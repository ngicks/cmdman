package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// StickyState is what [AttachSticky] reads between attach attempts to decide
// whether to call OpenSession again or jump straight to the wait prompt.
type StickyState struct {
	// Running is true when the command is currently startable into an attach
	// session (Starting or Running). When false, AttachSticky skips OpenSession
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
//   - opts.StdinPipe and opts.StdoutPipe are shared across iterations: each
//     [Attach] run and each wait prompt derives its own pipe and closes it on
//     exit, while the underlying stdin source and display sink stay open
//     throughout. Bytes pulled from the source but not yet delivered carry
//     over to the next derived pipe, so a keystroke racing an iteration
//     change is not lost.
//   - When the stdin source reaches EOF, the current consumer sees EOF and
//     any later wait prompt detaches: with no stdin left there is nobody to
//     answer it.
//   - AttachSticky never closes the source or the sink, and the caller owns
//     the controllers' Run lifecycle.
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

	// Once for the whole loop rather than per re-attach: a work dir deleted
	// while the loop sits at the wait prompt would otherwise be reported on
	// every restart cycle. Clearing it is what keeps the Attach calls below
	// from repeating it.
	chdirWorkDir(ctx, opts.WorkDir)
	opts.WorkDir = ""

	for {
		state, err := hooks.State(ctx)
		if err != nil {
			return err
		}

		if !state.Running {
			done, err := waitAtPrompt(ctx, hooks, opts, state.Status)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}

		session, err := hooks.OpenSession(ctx)
		if err != nil {
			// Re-opening the attach stream failed (e.g. the monitor is still
			// coming up after a restart). Surface it at the wait prompt rather
			// than spinning, so the user can retry with 'r' or detach.
			done, perr := waitAtPrompt(
				ctx, hooks, opts,
				fmt.Sprintf("open attach session failed: %v", err),
			)
			if perr != nil {
				return perr
			}
			if done {
				return nil
			}
			continue
		}

		err = Attach(ctx, session, opts)

		switch {
		case errors.Is(err, ErrRemoteEOF):
			// Command exited; loop back to prompt.
			continue
		case errors.Is(err, ErrForceExit):
			return err
		case ctx.Err() != nil:
			// Top-level shutdown (ctx canceled): exit.
			return err
		case err != nil:
			// A transient stream/transport error — e.g. the monitor tearing
			// the attach stream down while the command is stopped or
			// restarted — must NOT kill the sticky session and close its mux
			// pane. Surface it and drop to the wait prompt so the user can
			// restart ('r') or detach.
			done, perr := waitAtPrompt(
				ctx, hooks, opts,
				fmt.Sprintf("attach error: %v", err),
			)
			if perr != nil {
				return perr
			}
			if done {
				return nil
			}
			continue
		default:
			// Detach-keys path; user is done.
			return nil
		}
	}
}

// waitAtPrompt shows the wait prompt and applies its outcome. It returns
// done=true when the user chose to detach (the caller should return nil) and
// false to keep looping. A restart request is dispatched here; a restart
// failure is reported and treated as "stay at the prompt" on the next pass.
func waitAtPrompt(
	ctx context.Context,
	hooks StickyHooks,
	opts AttachOptions,
	status string,
) (done bool, err error) {
	result, err := promptWait(ctx, opts, status)
	if err != nil {
		return false, err
	}
	switch result {
	case PromptRestart:
		if rerr := hooks.Restart(ctx); rerr != nil {
			fmt.Fprintf(opts.Stdout, "\r\nrestart failed: %v\r\n", rerr)
		}
		return false, nil
	case PromptDetach:
		return true, nil
	}
	return false, nil
}

// PromptStickyWait blocks reading stdin in raw mode for 'r' / 'R' (restart),
// the configured detach-keys (detach), or ctx.Done() (detach with ctx.Err).
// It derives its stdin pipe from opts.StdinPipe (whose Run loop the caller
// must have started) and closes it on return. It is the single-shot building
// block exposed for tests; [AttachSticky] drives it inside its loop.
func PromptStickyWait(
	ctx context.Context,
	statusLine string,
	opts AttachOptions,
) (PromptResult, error) {
	if err := opts.validate(); err != nil {
		return 0, err
	}
	return promptWait(ctx, opts, statusLine)
}

// promptWait is the shared implementation used by [PromptStickyWait] and
// [AttachSticky]. It owns the raw-mode lifecycle for the prompt only.
func promptWait(
	ctx context.Context,
	opts AttachOptions,
	statusLine string,
) (PromptResult, error) {
	detachKeys, err := parseDetachKeys(opts.DetachKeys)
	if err != nil {
		return 0, fmt.Errorf("invalid detach-keys: %w", err)
	}

	sub, _, err := opts.StdinPipe.Pipe(ctx)
	switch {
	case errors.Is(err, io.EOF):
		// The stdin source is exhausted; nobody can answer the prompt, so
		// skip printing it and detach.
		return PromptDetach, nil
	case err != nil:
		if ctx.Err() != nil {
			return PromptDetach, ctx.Err()
		}
		return 0, err
	}
	defer sub.Close()

	restore := setupRawTerminal(false, opts.Stdin, opts.Stdout)
	defer restore()

	fmt.Fprintf(
		opts.Stdout,
		"\r\n[%s] press 'r' to restart, %s to detach\r\n",
		statusLine, opts.DetachKeys,
	)

	rdr := newDetachKeyReader(sub, detachKeys)

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
				if errors.Is(err, errDetach) {
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
