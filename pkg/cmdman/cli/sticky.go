package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/moby/term"
)

// StickyState is what [AttachSticky] reads between attach attempts to decide
// whether to call OpenSession again or jump straight to the wait prompt.
type StickyState struct {
	// Running is true when the command is currently startable into an attach
	// session (Starting or Started). When false, AttachSticky skips OpenSession
	// for this iteration and goes straight to the wait prompt.
	Running bool
	// Status is the human-readable status line shown in the wait prompt
	// (e.g. "exited (code 1)" / "not running"). Free-form.
	Status string
}

// StickyHooks wires [AttachSticky] to the cmdman service layer. Each hook is
// invoked from one goroutine and may be called multiple times across the
// sticky loop's lifetime.
type StickyHooks struct {
	// State returns the current command state. AttachSticky uses it to decide
	// running vs waiting, and as the prompt status line.
	State func(ctx context.Context) (StickyState, error)
	// OpenSession opens a fresh attach session against the command. Called
	// only when State reports Running == true.
	OpenSession func(ctx context.Context) (AttachSession, error)
	// Restart restarts the command. Called when the user picks 'r' at the
	// wait prompt. The next loop iteration will call State and then either
	// OpenSession (when the restart succeeded) or re-prompt.
	Restart func(ctx context.Context) error
}

// PromptResult is the outcome of [PromptStickyWait].
type PromptResult int

const (
	// PromptRestart indicates the user pressed 'r' / 'R'.
	PromptRestart PromptResult = iota + 1
	// PromptDetach indicates the user pressed the detach-keys sequence, the
	// stdin source closed, or ctx was canceled.
	PromptDetach
)

// AttachSticky runs an attach loop: open a session, run [Attach], and when
// the stream EOFs (the monitored command exited or the monitor went away) it
// shows a wait prompt instead of returning. The user picks 'r' to restart &
// re-attach, or the configured detach-keys to exit cleanly. Today's exit-
// on-EOF behavior is recovered by running [Attach] directly without this
// wrapper (i.e. the `--auto-exit` cobra flag).
//
// Lifecycle:
//   - opts.StdinPipe is consumed by an internal multiplexer for the duration
//     of AttachSticky. Per-iteration [Attach] calls receive sub-pipes; closing
//     them is safe (and expected — Attach does it on exit).
//   - opts.StdoutPipe is passed through to every iteration unchanged.
//     Callers should NOT close opts.StdoutPipe until AttachSticky returns.
//   - On return, AttachSticky stops the multiplexer pump but does NOT close
//     opts.StdinPipe; the caller owns its lifecycle.
func AttachSticky(
	ctx context.Context,
	hooks StickyHooks,
	opts AttachOptions,
) error {
	if err := opts.validate(); err != nil {
		return err
	}
	if hooks.State == nil || hooks.OpenSession == nil || hooks.Restart == nil {
		return errors.New("attach: AttachSticky requires all hooks set")
	}

	mux := newStdinMux(opts.StdinPipe)
	defer mux.Stop()

	for {
		state, err := hooks.State(ctx)
		if err != nil {
			return err
		}

		if !state.Running {
			result, err := promptWait(ctx, mux, opts, state.Status)
			if err != nil {
				return err
			}
			switch result {
			case PromptRestart:
				if err := hooks.Restart(ctx); err != nil {
					fmt.Fprintf(opts.Stdout, "\r\nrestart failed: %v\r\n", err)
					continue
				}
				continue
			case PromptDetach:
				return nil
			}
		}

		session, err := hooks.OpenSession(ctx)
		if err != nil {
			fmt.Fprintf(opts.Stdout, "\r\nopen attach session failed: %v\r\n", err)
			// Surface the failure into the prompt loop on the next pass.
			continue
		}

		iterOpts := opts
		iterOpts.StdinPipe = mux.subPipe()
		err = Attach(ctx, session, iterOpts)
		switch {
		case errors.Is(err, ErrRemoteEOF):
			// Command exited; loop back to prompt.
			continue
		case errors.Is(err, ErrForceExit):
			return err
		case err != nil:
			return err
		default:
			// Detach-keys path; user is done.
			return nil
		}
	}
}

// PromptStickyWait blocks reading stdin in raw mode for 'r' / 'R' (restart),
// the configured detach-keys (detach), or ctx.Done() (detach with ctx.Err).
// It is the single-shot building block exposed for tests; [AttachSticky]
// drives it inside its loop.
func PromptStickyWait(
	ctx context.Context,
	statusLine string,
	opts AttachOptions,
) (PromptResult, error) {
	if err := opts.validate(); err != nil {
		return 0, err
	}
	mux := newStdinMux(opts.StdinPipe)
	defer mux.Stop()
	return promptWait(ctx, mux, opts, statusLine)
}

// promptWait is the shared implementation used by [PromptStickyWait] and
// [AttachSticky]. It owns the raw-mode lifecycle for the prompt only.
func promptWait(
	ctx context.Context,
	mux *stdinMux,
	opts AttachOptions,
	statusLine string,
) (PromptResult, error) {
	detachKeys, err := parseDetachKeys(opts.DetachKeys)
	if err != nil {
		return 0, fmt.Errorf("invalid detach-keys: %w", err)
	}

	restore := setupRawTerminal(false, opts.Stdin, opts.Stdout)
	defer restore()

	fmt.Fprintf(
		opts.Stdout,
		"\r\n[%s] press 'r' to restart, %s to detach\r\n",
		statusLine, opts.DetachKeys,
	)

	sub := mux.subPipe()
	defer sub.Close()

	var rdr io.Reader = sub
	if len(detachKeys) > 0 {
		rdr = term.NewEscapeProxy(sub, detachKeys)
	}

	resultCh := make(chan PromptResult, 1)
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := rdr.Read(buf)
			for i := range n {
				if buf[i] == 'r' || buf[i] == 'R' {
					resultCh <- PromptRestart
					return
				}
			}
			if err != nil {
				if _, isEscape := errors.AsType[term.EscapeError](err); isEscape {
					resultCh <- PromptDetach
					return
				}
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
					resultCh <- PromptDetach
					return
				}
				errCh <- err
				return
			}
		}
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case err := <-errCh:
		return 0, err
	case <-ctx.Done():
		_ = sub.Close()
		return PromptDetach, ctx.Err()
	}
}

// stdinMux fans bytes from a single source reader out to a sequence of
// sub-pipes. Each sub-pipe is given to one consumer (an [Attach] iteration
// or a prompt). When a sub-pipe is closed, the mux moves on to the next.
//
// The pump goroutine runs for the lifetime of the mux. [Stop] is best-effort
// — closing the source is the only way to unblock a stdin Read, and the
// caller (typically [AttachSticky]) keeps source ownership.
type stdinMux struct {
	source io.Reader
	mu     sync.Mutex
	cur    io.WriteCloser
	stop   chan struct{}
	done   chan struct{}
}

func newStdinMux(source io.Reader) *stdinMux {
	m := &stdinMux{
		source: source,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go m.pump()
	return m
}

func (m *stdinMux) pump() {
	defer close(m.done)
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-m.stop:
			return
		default:
		}
		n, err := m.source.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			m.deliver(data)
		}
		if err != nil {
			return
		}
	}
}

// deliver writes data to the currently-active sub-pipe. If the write fails
// (sub-pipe closed mid-write), cur is cleared so the next iteration waits
// for a fresh sub-pipe via [subPipe].
func (m *stdinMux) deliver(data []byte) {
	m.mu.Lock()
	w := m.cur
	m.mu.Unlock()
	if w == nil {
		return // no active consumer; drop the byte
	}
	if _, err := w.Write(data); err != nil {
		m.mu.Lock()
		if m.cur == w {
			m.cur = nil
		}
		m.mu.Unlock()
	}
}

// subPipe installs a fresh io.Pipe as the active consumer and returns its
// read end. The previous sub-pipe (if any) is closed.
func (m *stdinMux) subPipe() io.ReadCloser {
	pr, pw := io.Pipe()
	m.mu.Lock()
	prev := m.cur
	m.cur = pw
	m.mu.Unlock()
	if prev != nil {
		_ = prev.Close()
	}
	return pr
}

// Stop signals the pump to stop on its next iteration. The source reader is
// left open — only the caller may close it. Stop does not wait for the pump
// to actually exit; it is safe to call even if the pump is blocked on a
// source Read.
func (m *stdinMux) Stop() {
	close(m.stop)
	m.mu.Lock()
	if m.cur != nil {
		_ = m.cur.Close()
		m.cur = nil
	}
	m.mu.Unlock()
}
