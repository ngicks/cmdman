package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestForwardedSignals_DoesNotIncludeSIGHUP(t *testing.T) {
	assert.Assert(
		t,
		!slices.ContainsFunc(
			forwardedSignals,
			func(sig os.Signal) bool { return sig == syscall.SIGHUP },
		),
	)
}

func TestForwardedSignals_DoesNotIncludeSIGURG(t *testing.T) {
	assert.Assert(
		t,
		!slices.ContainsFunc(
			forwardedSignals,
			func(sig os.Signal) bool { return sig == syscall.SIGURG },
		),
	)
}

func TestPumpStdinToStream_ForwardsMultilineBracketedPaste(t *testing.T) {
	input := []byte("\x1b[200~first\nsecond\nthird\n\x1b[201~")
	session := &recordingAttachSession{}
	errCh := make(chan error, 1)

	pumpStdinToStream(bytes.NewReader(input), session, []byte{0x10, 0x11}, errCh)

	assert.Equal(t, string(session.stdin), string(input))
	assert.Equal(t, len(errCh), 0)
}

func TestPumpStdinToStream_ForwardsBeforeDetach(t *testing.T) {
	session := &recordingAttachSession{}
	errCh := make(chan error, 1)

	pumpStdinToStream(bytes.NewReader([]byte("hello\x10\x11")), session, []byte{0x10, 0x11}, errCh)

	assert.Equal(t, string(session.stdin), "hello")
	err := <-errCh
	ok := errors.Is(err, errDetach)
	assert.Assert(t, ok)
}

// TestAttach_DefaultDetachKeysInterceptedNotForwarded is the regression guard
// for the TUI attach path (serviceBackend.Attach), which previously left
// DetachKeys empty. With it unset the detach sequence (Ctrl-P, Ctrl-Q =
// 0x10,0x11) was forwarded straight into the remote command's PTY — Ctrl-Q is
// XON — disrupting interactive programs (claude/codex) and tearing the session
// down. Driving the real Attach with DefaultDetachKeys (the constant the TUI
// path now sets) proves the sequence is consumed locally: Attach returns nil
// (graceful detach) and the detach bytes never reach the session.
func TestAttach_DefaultDetachKeysInterceptedNotForwarded(t *testing.T) {
	// Stdin/Stdout are only used for terminal probing; pipes are not terminals,
	// so raw mode and resize are skipped and nothing is written to them.
	stdinR, stdinW, err := os.Pipe()
	assert.NilError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	assert.NilError(t, err)
	t.Cleanup(func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
	})

	session := newBlockingAttachSession()
	opts := AttachOptions{
		DetachKeys: DefaultDetachKeys,
		Stdin:      stdinR,
		Stdout:     stdoutW,
		StdinPipe:  io.NopCloser(bytes.NewReader([]byte("hello\x10\x11"))),
		StdoutPipe: nopWriteCloser{io.Discard},
	}

	// A bounded context makes the buggy case (detach not intercepted, so the
	// stdin pump forwards everything and never signals) fail fast on the
	// SendStdin assertion below instead of hanging until the test deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = Attach(ctx, session, opts)
	assert.NilError(t, err) // detach-keys are a graceful exit (nil), not ErrRemoteEOF.

	// Only the literal bytes before the sequence are forwarded; 0x10,0x11 are
	// swallowed by Attach and never sent to the remote command.
	assert.Equal(t, string(session.sentStdin()), "hello")
}

func TestRestoreDisplayModes(t *testing.T) {
	var buf bytes.Buffer
	restoreDisplayModes(&buf)

	assert.Equal(t, buf.String(), displayModeResetSeq)
}

type recordingAttachSession struct {
	stdin []byte
}

func (s *recordingAttachSession) Recv() ([]byte, error) {
	return nil, io.EOF
}

func (s *recordingAttachSession) SendStdin(data []byte) error {
	s.stdin = append(s.stdin, data...)
	return nil
}

func (s *recordingAttachSession) Signal(context.Context, int32) error {
	return nil
}

func (s *recordingAttachSession) Resize(int, int) error {
	return nil
}

func (s *recordingAttachSession) CloseSend() error {
	return nil
}

func (s *recordingAttachSession) Close() error {
	return nil
}

// blockingAttachSession records SendStdin and blocks Recv until Close, so the
// stdin pump — not a premature remote EOF — drives the attach lifecycle in
// TestAttach_DefaultDetachKeysInterceptedNotForwarded.
type blockingAttachSession struct {
	mu     sync.Mutex
	stdin  []byte
	once   sync.Once
	closed chan struct{}
}

func newBlockingAttachSession() *blockingAttachSession {
	return &blockingAttachSession{closed: make(chan struct{})}
}

func (s *blockingAttachSession) Recv() ([]byte, error) {
	<-s.closed
	return nil, io.EOF
}

func (s *blockingAttachSession) SendStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stdin = append(s.stdin, data...)
	return nil
}

func (s *blockingAttachSession) Signal(context.Context, int32) error { return nil }
func (s *blockingAttachSession) Resize(int, int) error               { return nil }
func (s *blockingAttachSession) CloseSend() error                    { return nil }

func (s *blockingAttachSession) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *blockingAttachSession) sentStdin() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.stdin)
}
