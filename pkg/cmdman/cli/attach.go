// Package cli holds CLI-presentation helpers for the cmdman binary —
// terminal control, key parsing, table rendering, and other display logic
// that lives outside the wiring layer under ./cmd.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
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

// errDetach is the sentinel [detachKeyReader] returns once the detach-key
// sequence is seen. It is package-internal: both the attach and sticky-prompt
// readers signal detach with it, and callers consume it as a graceful exit
// (Attach swallows it, the prompt maps it to [PromptDetach]) so it never
// escapes the package.
var errDetach = errors.New("attach: detach-keys pressed")

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

// DefaultDetachKeys is the detach-key sequence used when a caller does not
// override it: Ctrl-P, Ctrl-Q. Intercepted locally by Attach and never
// forwarded to the remote command.
const DefaultDetachKeys = "ctrl-p,ctrl-q"

// AttachOptions configure a single attach run. All four I/O fields are
// required; Attach does not fall back to os.Stdin / os.Stdout.
//
// Stdin and Stdout are the raw *os.File handles used to inspect and
// modify terminal state (term.IsTerminal probing, raw-mode toggling,
// SIGWINCH ioctl). They are never read from or written to as byte
// streams.
//
// StdinPipe and StdoutPipe carry the byte streams. Typically they are
// cancellable io.Pipe wrappers (see internal/stdiopipe) around
// Stdin/Stdout so the attach loop can unblock pending Read/Write calls
// by closing them.
type AttachOptions struct {
	NoStdin    bool
	SigProxy   bool
	DetachKeys string

	// PauseSignals and ResumeSignals bracket Attach's signal forwarding so the
	// process-global SIGINT/SIGTERM handler installed by the binary does not
	// also fire while attached.
	//
	// When SigProxy is set and both hooks are non-nil, Attach calls
	// PauseSignals(install) — where install registers Attach's own forwarding
	// handler — before forwarding begins, and ResumeSignals(remove) — where
	// remove unregisters it — once forwarding stops. The command layer wires
	// these to cmdsignals.Pause / cmdsignals.Resume bound to the root context,
	// so SIGINT/SIGTERM reach the remote command during an attach and normal
	// CLI cancellation is restored on detach. Pause's install / Resume's remove
	// run atomically with the suspend / restore, leaving no window where the
	// signals are unhandled.
	//
	// Both nil (the TUI and test callers) means Attach installs its forwarding
	// handler directly and never touches the global handler.
	PauseSignals  func(install func()) bool
	ResumeSignals func(remove func()) bool

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

	sendResize(session, opts.Stdout)

	var wg sync.WaitGroup
	forceExitCh := make(chan struct{})

	if opts.SigProxy {
		sigCh := make(chan os.Signal, 4)
		install := func() { signal.Notify(sigCh, forwardedSignals...) }
		remove := func() { signal.Stop(sigCh) }

		// Suspend the binary's process-global SIGINT/SIGTERM handler for the
		// duration of the attach so those signals are forwarded to the remote
		// command instead of cancelling the CLI, then restore it on return.
		// PauseSignals installs our forwarding handler atomically with the
		// suspension (and ResumeSignals removes it on the way out). When the
		// hooks are unset (TUI / tests) we forward directly and never touch the
		// global handler. Pausing — rather than signal.Reset — leaves SIGPIPE
		// and every other unrelated signal trapped as gRPC's runtime requires.
		if opts.PauseSignals != nil && opts.ResumeSignals != nil && opts.PauseSignals(install) {
			defer opts.ResumeSignals(remove)
		} else {
			install()
			defer remove()
		}

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
		isEscape := errors.Is(err, errDetach)
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
	stdinFd := int(stdin.Fd())
	if !term.IsTerminal(stdinFd) {
		return func() {}
	}
	savedState, err := term.MakeRaw(stdinFd)
	if err != nil {
		return func() {}
	}
	return sync.OnceFunc(func() {
		_ = term.Restore(stdinFd, savedState)
		// stdin and stdout can differ in odd half-interactive setups
		// such as `cmd attach ID | tee out.log`: stdin is still the
		// user's tty, so termios restore is valid, while stdout is a
		// pipe, so writing display-reset escapes there would just emit
		// junk into the pipeline.
		if term.IsTerminal(int(stdout.Fd())) {
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

// detachKeyReader wraps stdin and scans for the detach-key sequence, returning
// errDetach once the full sequence is seen. Bytes that partially matched
// the sequence but then diverged are forwarded verbatim, in order. Literal
// (non-matching) bytes are copied straight into the caller's buffer; pending is
// only ever the bounded overflow from flushing a matched prefix that did not
// fit, so it never grows past len(detachKey).
type detachKeyReader struct {
	r         *bufio.Reader
	detachKey []byte
	match     int
	pending   []byte
	detached  bool
}

func newDetachKeyReader(r io.Reader, detachKeys []byte) io.Reader {
	if len(detachKeys) == 0 {
		return r
	}
	return &detachKeyReader{
		r:         bufio.NewReaderSize(r, 32*1024),
		detachKey: append([]byte(nil), detachKeys...),
	}
}

func (r *detachKeyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	n := 0
	if len(r.pending) > 0 {
		copied := copy(p, r.pending)
		n += copied
		if copied < len(r.pending) {
			r.pending = r.pending[copied:]
			return n, nil
		}
		r.pending = r.pending[:0]
	}
	if r.detached {
		return n, errDetach
	}

	for n < len(p) {
		// Respect io.Reader semantics: once we have bytes for the caller and
		// nothing more is immediately buffered, return instead of blocking on
		// another ReadByte to fill p. Any partial match is carried in r.match
		// across calls, so this never drops or reorders input. Without this a
		// single keystroke would stall until the buffer filled or detach hit.
		if n > 0 && r.r.Buffered() == 0 {
			return n, nil
		}

		// Fast path: with no partial match in progress, bulk-copy the run of
		// already-buffered bytes up to the next possible detach-key start.
		if r.match == 0 && r.r.Buffered() > 0 {
			chunk, _ := r.r.Peek(r.r.Buffered())
			run := chunk
			if i := bytes.IndexByte(chunk, r.detachKey[0]); i >= 0 {
				run = chunk[:i]
			}
			if len(run) > 0 {
				copied := copy(p[n:], run)
				_, _ = r.r.Discard(copied)
				n += copied
				continue
			}
		}

		b, err := r.r.ReadByte()
		if err != nil {
			if r.match > 0 {
				n = r.emit(p, n, r.detachKey[:r.match])
				r.match = 0
			}
			if n > 0 {
				return n, nil
			}
			return 0, err
		}

		if b == r.detachKey[r.match] {
			r.match++
			if r.match == len(r.detachKey) {
				r.match = 0
				r.detached = true
				return n, errDetach
			}
			continue
		}

		// b diverges from detachKey[r.match]: flush the matched prefix, then
		// reconsider b as either the start of a fresh match or a literal byte.
		if r.match > 0 {
			matched := r.match
			r.match = 0
			n = r.emit(p, n, r.detachKey[:matched])
			if b == r.detachKey[0] {
				r.match = 1
				if len(r.pending) > 0 || n == len(p) {
					return n, nil
				}
				continue
			}
		}
		if len(r.pending) > 0 || n == len(p) {
			r.pending = append(r.pending, b)
			return n, nil
		}
		p[n] = b
		n++
	}
	return n, nil
}

// emit copies src into p starting at offset n, spilling any remainder that does
// not fit into r.pending, and returns the new offset.
func (r *detachKeyReader) emit(p []byte, n int, src []byte) int {
	copied := copy(p[n:], src)
	if copied < len(src) {
		r.pending = append(r.pending, src[copied:]...)
	}
	return n + copied
}

func sendResize(session AttachSession, stdout *os.File) {
	rows, cols := terminalSize(stdout)
	if rows > 0 && cols > 0 {
		_ = session.Resize(rows, cols)
	}
}

// ctrlKeyBytes maps the key part of a control-key token (the character after a
// "ctrl-"/"c-" prefix) to the ASCII control byte it produces: the 0x00..0x1f
// block, i.e. @ a-z [ \ ] ^ _. Keys are lower-cased because parseDetachKeys
// lower-cases input before lookup. Edit this table to add or change a mapping.
var ctrlKeyBytes = map[byte]byte{
	'@': 0x00,
	'a': 0x01, 'b': 0x02, 'c': 0x03, 'd': 0x04, 'e': 0x05, 'f': 0x06, 'g': 0x07,
	'h': 0x08, 'i': 0x09, 'j': 0x0a, 'k': 0x0b, 'l': 0x0c, 'm': 0x0d, 'n': 0x0e, 'o': 0x0f,
	'p': 0x10, 'q': 0x11, 'r': 0x12, 's': 0x13, 't': 0x14, 'u': 0x15, 'v': 0x16, 'w': 0x17,
	'x': 0x18, 'y': 0x19, 'z': 0x1a,
	'[': 0x1b, '\\': 0x1c, ']': 0x1d, '^': 0x1e, '_': 0x1f,
}

// detachKeyPrefixes is the nested lookup table for "<prefix><key>" detach
// tokens: the outer key is the spelled prefix, the inner table maps the single
// key character to its byte. ctrl- and the tmux-style C- share one inner table,
// so teaching the parser a new spelling is a single extra row. (Lower-cased;
// see parseDetachKeys.)
var detachKeyPrefixes = map[string]map[byte]byte{
	"ctrl-": ctrlKeyBytes,
	"c-":    ctrlKeyBytes,
}

// parseDetachKeys parses a detach-key sequence string into the raw byte
// sequence that signals detach. Tokens are comma-separated; each is either a
// single literal character or a control key spelled "ctrl-<c>" or the tmux-
// style "C-<c>" (case-insensitive), where <c> is one of @ A-Z [ \ ] ^ _ (the
// 0x00..0x1f control range). An empty string disables detach.
//
// e.g. "ctrl-p,ctrl-q" and "C-p,C-q" both parse to {0x10, 0x11}.
func parseDetachKeys(detachKeys string) ([]byte, error) {
	if detachKeys == "" {
		return nil, nil
	}
	var codes []byte
	for token := range strings.SplitSeq(strings.ToLower(detachKeys), ",") {
		code, err := parseDetachKeyToken(token)
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

// parseDetachKeyToken resolves one token through the nested prefix/key table. A
// single character is a literal byte; otherwise the token splits into its last
// character (the key) and everything before it (the prefix), and both must hit
// the table. token is assumed already lower-cased.
func parseDetachKeyToken(token string) (byte, error) {
	if len(token) == 1 {
		return token[0], nil
	}
	prefix, key := token[:len(token)-1], token[len(token)-1]
	keys, ok := detachKeyPrefixes[prefix]
	if !ok {
		return 0, fmt.Errorf("invalid detach key %q", token)
	}
	code, ok := keys[key]
	if !ok {
		return 0, fmt.Errorf("detach key %q is not a control character", token)
	}
	return code, nil
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
