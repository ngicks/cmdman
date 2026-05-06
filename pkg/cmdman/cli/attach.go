// Package cli holds CLI-presentation helpers for the cmdman binary —
// terminal control, key parsing, table rendering, and other display logic
// that lives outside the wiring layer under ./cmd.
package cli

import (
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
type AttachSession interface {
	Recv() ([]byte, error)
	SendStdin([]byte) error
	Signal(ctx context.Context, sig int32) error
	Resize(rows, cols int) error
	CloseSend() error
}

// AttachOptions configure a single attach run.
type AttachOptions struct {
	// NoStdin disables stdin forwarding (output-only mode).
	NoStdin bool
	// SigProxy forwards signals to the remote command and tracks 3-press
	// force-exit on SIGINT/SIGTERM.
	SigProxy bool
	// DetachKeys is the raw key sequence (e.g. "ctrl-p,ctrl-q") that
	// detaches the session when typed on stdin. Empty disables detection.
	DetachKeys string
	// ResetSignals are signals whose handlers should be cleared before the
	// attach loop installs its own forwarding handler. Typically the same
	// set passed to signal.NotifyContext at process start, so that the
	// attach loop can take exclusive ownership of those signals while it
	// runs.
	ResetSignals []os.Signal
	// Stdin is the input source. Defaults to os.Stdin when nil.
	Stdin *os.File
	// Stdout is the output sink. Defaults to os.Stdout when nil.
	Stdout *os.File
}

// Attach runs the attach loop against session: terminal raw mode, signal
// forwarding, stdin/stdout multiplexing, and detach-key detection. It
// returns when the remote stream ends, the user types the detach sequence,
// the context is cancelled, or the user requests force exit.
//
// Returns ErrForceExit when the user pressed SIGINT/SIGTERM three times in
// a row; the terminal has already been restored.
func Attach(ctx context.Context, session AttachSession, opts AttachOptions) error {
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	detachKeys, err := parseDetachKeys(opts.DetachKeys)
	if err != nil {
		return fmt.Errorf("invalid detach-keys: %w", err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	restoreTerminal := setupRawTerminal(opts.NoStdin, stdin, stdout)
	defer restoreTerminal()

	if len(opts.ResetSignals) > 0 {
		// We cannot use signal.Reset() with no arguments because gRPC's
		// runtime relies on SIGPIPE staying trapped.
		signal.Reset(opts.ResetSignals...)
	}

	sendResize(session)

	forceExitCh := make(chan struct{})

	if opts.SigProxy {
		sigCh := make(chan os.Signal, 4)
		signal.Notify(sigCh, forwardedSignals...)
		defer signal.Stop(sigCh)

		go handleAttachSignals(attachCtx, sigCh, session, forceExitCh)
	}

	errCh := make(chan error, 2)
	go pumpStreamToStdout(session, stdout, errCh)
	if !opts.NoStdin {
		go pumpStdinToStream(stdin, session, detachKeys, errCh)
	}

	var exitErr error
	select {
	case err := <-errCh:
		if _, isEscape := errors.AsType[term.EscapeError](err); err != io.EOF && !isEscape {
			exitErr = err
		}
	case <-forceExitCh:
		exitErr = ErrForceExit
	case <-ctx.Done():
	}

	cancel()
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
				sendResize(session)
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
		r = term.NewEscapeProxy(stdin, detachKeys)
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

func sendResize(session AttachSession) {
	rows, cols := terminalSizeImpl()
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
