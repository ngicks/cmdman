// Package cli holds CLI-presentation helpers for the cmdman binary —
// terminal control, key parsing, table rendering, and other display logic
// that lives outside the wiring layer under ./cmd.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/moby/term"
)

// ErrForceExit indicates the user pressed the force-exit signal sequence
// (3 consecutive SIGINT/SIGTERM) during an attach session. The terminal has
// already been restored before this error is returned. Callers at the binary
// boundary should propagate it so the process exits non-zero.
var ErrForceExit = errors.New("attach: force exit requested")

// ErrRemoteEOF indicates the remote attach stream closed gracefully (the
// monitored command exited or the monitor went away). Distinguished from
// the detach-keys path (which still returns nil) so the sticky-attach loop
// in [AttachSticky] can prompt the user for restart.
var ErrRemoteEOF = errors.New("attach: remote stream closed")

// forwardedSignals are forwarded to the remote command during an attach
// session.
var forwardedSignals = []os.Signal{
	syscall.SIGINT,
	syscall.SIGTERM,
	syscall.SIGQUIT,
	syscall.SIGUSR1,
	syscall.SIGUSR2,
	syscall.SIGTSTP,
	syscall.SIGCONT,
	syscall.SIGWINCH,
}

// AttachSession is the minimal interface the attach loop needs from a remote
// session. *cmdman.Session satisfies it.
//
// Close is invoked by Attach on shutdown to unblock a pending Recv. It
// must be safe to call alongside (or before) any defer Close the caller
// wires up — the underlying grpc.ClientConn.Close returns an error on a
// second call but does not panic.
type AttachSession interface {
	Recv() ([]byte, error)
	SendStdin([]byte) error
	Signal(ctx context.Context, sig int32) error
	Resize(rows, cols int) error
	CloseSend() error
	Close() error
}

// AttachOptions configure a single attach run. All four I/O fields are
// required; Attach does not fall back to os.Stdin / os.Stdout.
//
// Stdin and Stdout are the raw *os.File handles used to inspect and
// modify terminal state (term.IsTerminal probing, raw-mode toggling,
// SIGWINCH ioctl). They are never read from or written to as byte
// streams.
//
// StdinPipe and StdoutPipe carry the byte streams. Typically they are
// cancellable io.Pipe wrappers (see cmd/internal/stdiopipe) around
// Stdin/Stdout so the attach loop can unblock pending Read/Write calls
// by closing them.
type AttachOptions struct {
	NoStdin      bool
	SigProxy     bool
	DetachKeys   string
	ResetSignals []os.Signal

	Stdin      *os.File
	Stdout     *os.File
	StdinPipe  io.ReadCloser
	StdoutPipe io.WriteCloser
}

func (o AttachOptions) validate() error {
	switch {
	case o.Stdin == nil:
		return errors.New("attach: Stdin is required")
	case o.Stdout == nil:
		return errors.New("attach: Stdout is required")
	case o.StdinPipe == nil:
		return errors.New("attach: StdinPipe is required")
	case o.StdoutPipe == nil:
		return errors.New("attach: StdoutPipe is required")
	}
	return nil
}

// Attach runs the attach loop against session: terminal raw mode, signal
// forwarding, stdin/stdout multiplexing, and detach-key detection.
//
// All goroutines started by Attach are joined before it returns. Attach
// triggers their termination by canceling its internal context, closing
// the session, and closing StdinPipe / StdoutPipe.
//
// Returns ErrForceExit when the user pressed SIGINT/SIGTERM three times
// in a row; the terminal has already been restored.
func Attach(ctx context.Context, session AttachSession, opts AttachOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}

	detachKeys, err := parseDetachKeys(opts.DetachKeys)
	if err != nil {
		return fmt.Errorf("invalid detach-keys: %w", err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	restoreTerminal := setupRawTerminal(opts.NoStdin, opts.Stdin, opts.Stdout)
	defer restoreTerminal()

	if len(opts.ResetSignals) > 0 {
		// We cannot use signal.Reset() with no arguments because gRPC's
		// runtime relies on SIGPIPE staying trapped.
		signal.Reset(opts.ResetSignals...)
	}

	sendResize(session, opts.Stdout)

	var wg sync.WaitGroup
	forceExitCh := make(chan struct{})

	if opts.SigProxy {
		sigCh := make(chan os.Signal, 4)
		signal.Notify(sigCh, forwardedSignals...)
		defer signal.Stop(sigCh)

		wg.Go(func() {
			handleAttachSignals(attachCtx, sigCh, session, opts.Stdout, forceExitCh)
		})
	}

	errCh := make(chan error, 2)
	wg.Go(func() {
		pumpStreamToStdout(session, opts.StdoutPipe, errCh)
	})
	if !opts.NoStdin {
		wg.Go(func() {
			pumpStdinToStream(opts.StdinPipe, session, detachKeys, errCh)
		})
	}

	var exitErr error
	select {
	case err := <-errCh:
		_, isEscape := errors.AsType[term.EscapeError](err)
		switch {
		case isEscape:
			// User pressed detach-keys; treat as a graceful exit.
		case err == io.EOF:
			// Remote stream closed (command exited / monitor gone).
			exitErr = ErrRemoteEOF
		default:
			exitErr = err
		}
	case <-forceExitCh:
		exitErr = ErrForceExit
	case <-ctx.Done():
	}

	// Trigger goroutine termination, then join them all before returning.
	cancel()                    // signal handler exits via attachCtx.Done
	_ = session.Close()         // pumpStreamToStdout: Recv errors out
	_ = opts.StdinPipe.Close()  // pumpStdinToStream: Read returns io.EOF / closed-pipe
	_ = opts.StdoutPipe.Close() // unblocks any pending Write in pumpStreamToStdout
	wg.Wait()

	_ = session.CloseSend()
	restoreTerminal()

	return exitErr
}

// setupRawTerminal puts stdin into raw mode (when applicable) and returns
// the deferred restore function. The returned closure is wrapped in
// sync.OnceFunc so callers can invoke it both via defer and explicitly
// without double-restoring.
func setupRawTerminal(noStdin bool, stdin, stdout *os.File) func() {
	if noStdin {
		return func() {}
	}
	stdinFd := stdin.Fd()
	if !term.IsTerminal(stdinFd) {
		return func() {}
	}
	savedState, err := term.SetRawTerminal(stdinFd)
	if err != nil {
		return func() {}
	}
	return sync.OnceFunc(func() {
		_ = term.RestoreTerminal(stdinFd, savedState)
		// stdin and stdout can differ in odd half-interactive setups
		// such as `cmd attach ID | tee out.log`: stdin is still the
		// user's tty, so termios restore is valid, while stdout is a
		// pipe, so writing display-reset escapes there would just emit
		// junk into the pipeline.
		if term.IsTerminal(stdout.Fd()) {
			restoreDisplayModes(stdout)
		}
	})
}

// handleAttachSignals processes signals during an attach session:
//   - SIGWINCH → send a resize event
//   - SIGINT/SIGTERM → forward to remote; close forceExitCh after 3 in a row
//   - all others → forward to remote
func handleAttachSignals(
	ctx context.Context,
	sigCh <-chan os.Signal,
	session AttachSession,
	stdout *os.File,
	forceExitCh chan<- struct{},
) {
	var once sync.Once
	forceCount := 0
	for {
		select {
		case sig, ok := <-sigCh:
			if !ok {
				return
			}
			sigNum, ok := sig.(syscall.Signal)
			if !ok {
				continue
			}

			if sigNum == syscall.SIGWINCH {
				sendResize(session, stdout)
				forceCount = 0
				continue
			}

			_ = session.Signal(ctx, int32(sigNum))

			if sigNum == syscall.SIGINT || sigNum == syscall.SIGTERM {
				forceCount++
				if forceCount >= 3 {
					once.Do(func() { close(forceExitCh) })
					return
				}
			} else {
				forceCount = 0
			}

		case <-ctx.Done():
			return
		}
	}
}

func pumpStreamToStdout(session AttachSession, stdout io.Writer, errCh chan<- error) {
	for {
		data, err := session.Recv()
		if err != nil {
			errCh <- err
			return
		}
		_, _ = stdout.Write(data)
	}
}

func pumpStdinToStream(
	stdin io.Reader,
	session AttachSession,
	detachKeys []byte,
	errCh chan<- error,
) {
	r := stdin
	if len(detachKeys) > 0 {
		r = newDetachKeyReader(stdin, detachKeys)
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			if sendErr := session.SendStdin(data); sendErr != nil {
				errCh <- sendErr
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				errCh <- err
			}
			return
		}
	}
}

type detachKeyReader struct {
	r         *bufio.Reader
	detachKey []byte
	match     int
	pending   []byte
	detached  bool
}

func newDetachKeyReader(r io.Reader, detachKeys []byte) io.Reader {
	return &detachKeyReader{
		r:         bufio.NewReaderSize(r, 32*1024),
		detachKey: append([]byte(nil), detachKeys...),
	}
}

func (r *detachKeyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	var n int
	for n < len(p) {
		if len(r.pending) > 0 {
			copied := copy(p[n:], r.pending)
			n += copied
			r.pending = r.pending[copied:]
			continue
		}
		if r.detached {
			return n, term.EscapeError{}
		}

		b, err := r.r.ReadByte()
		if err == nil {
			if r.scanByte(b) {
				return n, term.EscapeError{}
			}
			continue
		}
		if r.match > 0 {
			r.pending = append(r.pending, r.detachKey[:r.match]...)
			r.match = 0
			continue
		}
		if n > 0 {
			return n, nil
		}
		return 0, err
	}
	return n, nil
}

func (r *detachKeyReader) scanByte(b byte) bool {
	if b == r.detachKey[r.match] {
		r.match++
		if r.match == len(r.detachKey) {
			r.detached = true
			r.match = 0
			return true
		}
		return false
	}

	if r.match > 0 {
		r.pending = append(r.pending, r.detachKey[:r.match]...)
		r.match = 0
		if b == r.detachKey[0] {
			r.match = 1
			return false
		}
	}
	r.pending = append(r.pending, b)
	return false
}

func sendResize(session AttachSession, stdout *os.File) {
	rows, cols := terminalSize(stdout)
	if rows > 0 && cols > 0 {
		_ = session.Resize(rows, cols)
	}
}

// parseDetachKeys parses a detach-key sequence string (e.g. "ctrl-p,ctrl-q")
// into the raw byte sequence that signals detach.
func parseDetachKeys(detachKeys string) ([]byte, error) {
	if detachKeys == "" {
		return nil, nil
	}
	return term.ToBytes(strings.ToLower(detachKeys))
}

// restoreDisplayModes resets tty-driven display state that the attached
// program may have left behind. Terminal state restore only restores
// termios, not screen modes.
//
// This remains heuristic: attach is cleaning up after arbitrary programs
// whose terminal feature set we do not control or track.
//
// Side note: Bubble Tea's (*Program).restoreTerminal is narrower and
// state-driven. It only emits cleanup for modes Bubble Tea knows it
// enabled, for example \033[?1049l when alt screen was active, \033[?2004l
// when bracketed paste was enabled, and mouse-disable sequences when mouse
// mode was enabled. attach differs because it is cleaning up after
// arbitrary remote programs whose terminal state it did not enable and
// cannot observe, so it falls back to a broader unconditional best-effort
// cleanup sequence.
func restoreDisplayModes(w io.Writer) {
	_, _ = io.WriteString(w, displayModeResetSeq)
}

const displayModeResetSeq = "" +
	"\033[0m" + // Reset SGR (colors/bold).
	"\033[?25h" + // Show cursor.
	"\033[?1l" + // Leave application cursor-key mode.
	"\033[?1000l" + // Disable normal mouse tracking.
	"\033[?1002l" + // Disable button-event mouse tracking.
	"\033[?1003l" + // Disable any-event mouse tracking.
	"\033[?1004l" + // Disable focus reporting.
	"\033[?1006l" + // Disable SGR mouse reporting.
	"\033[?1015l" + // Disable urxvt mouse reporting.
	"\033[?2004l" + // Disable bracketed paste.
	"\033[?1049l" + // Leave the alternate screen buffer.
	"\033>\r\n" // Leave application keypad mode and move to a fresh shell line.
