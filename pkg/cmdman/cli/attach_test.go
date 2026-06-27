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

func TestParseDetachKeys(t *testing.T) {
	tests := []struct {
		input    string
		expected []byte
		wantErr  bool
	}{
		{"ctrl-p,ctrl-q", []byte{0x10, 0x11}, false},
		{"ctrl-a", []byte{0x01}, false},
		{"ctrl-z", []byte{0x1a}, false},
		{"ctrl-P,ctrl-Q", []byte{0x10, 0x11}, false},
		// tmux-style C- prefix, case-insensitive, mixable with ctrl-.
		{"C-p,C-q", []byte{0x10, 0x11}, false},
		{"c-a", []byte{0x01}, false},
		{"ctrl-p,C-q", []byte{0x10, 0x11}, false},
		// control-range edges: @=0x00, [=ESC 0x1b, _=0x1f.
		{"ctrl-@", []byte{0x00}, false},
		{"C-@", []byte{0x00}, false},
		{"ctrl-[", []byte{0x1b}, false},
		{"C-[", []byte{0x1b}, false},
		{"ctrl-_", []byte{0x1f}, false},
		// bare single char stays literal (distinct from its control form).
		{"a", []byte{'a'}, false},
		{"a,b,c", []byte{'a', 'b', 'c'}, false},
		{"@", []byte{'@'}, false},
		{"", nil, false},
		{"ctrl-", nil, true},
		{"ctrl-ab", nil, true},
		{"ctrl-1", nil, true},
		{"C-", nil, true},
		{"C-ab", nil, true},
		{"c-1", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDetachKeys(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error for %q", tt.input)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.expected)
		})
	}
}

func TestDetachKeys_ProxyDetectsSequence(t *testing.T) {
	input := []byte("hello\x10\x11")
	r := newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11})

	buf := make([]byte, 1024)
	var output []byte
	var detached bool
	for {
		n, err := r.Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if errors.Is(err, errDetach) {
			detached = true
			break
		}
		if err != nil {
			break
		}
	}

	assert.Assert(t, detached, "expected detach")
	assert.Equal(t, string(output), "hello")
}

func TestDetachKeys_ProxyPartialMatchFlush(t *testing.T) {
	input := []byte("\x10a")
	r := newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11})

	var output []byte
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			output = append(output, buf[:n]...)
		}
		if errors.Is(err, errDetach) {
			t.Fatal("should not detach")
		}
		if err != nil {
			break
		}
	}

	assert.Equal(t, string(output), "\x10a")
}

func TestDetachKeys_ProxyNoSequence(t *testing.T) {
	input := []byte("hello world")
	r := newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11})

	data, err := io.ReadAll(r)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "hello world")
}

func TestDetachKeys_ProxyOnlySequence(t *testing.T) {
	input := []byte{0x10, 0x11}
	r := newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11})

	buf := make([]byte, 1024)
	n, err := r.Read(buf)
	ok := errors.Is(err, errDetach)
	assert.Equal(t, n, 0)
	assert.Assert(t, ok)
}

// drainDetachKeyReader reads r to completion through a fixed-size buffer,
// exercising the cross-Read carry of partial matches and pending overflow. It
// returns everything forwarded and whether the detach sequence was hit.
func drainDetachKeyReader(t *testing.T, r io.Reader, bufSize int) (string, bool) {
	t.Helper()
	var out []byte
	buf := make([]byte, bufSize)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err == nil {
			continue
		}
		if errors.Is(err, errDetach) {
			return string(out), true
		}
		assert.Equal(t, err, io.EOF)
		return string(out), false
	}
}

func TestDetachKeyReader_ReassemblesThroughTinyBuffer(t *testing.T) {
	// A single-byte buffer forces every partial-match carry and pending spill
	// to cross a Read boundary.
	for _, bufSize := range []int{1, 2, 3, 7, 4096} {
		input := []byte("a\x10b\x10\x10\x11rest")
		out, detached := drainDetachKeyReader(
			t,
			newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11}),
			bufSize,
		)
		// The first 0x10 is a lone false start (followed by 'b'); the second
		// 0x10 is also a false start (followed by 0x10); the final 0x10,0x11
		// is the detach sequence, so "rest" is never forwarded.
		assert.Equal(t, out, "a\x10b\x10", "bufSize=%d", bufSize)
		assert.Assert(t, detached, "bufSize=%d", bufSize)
	}
}

func TestDetachKeyReader_ForwardsTrailingPartialMatchAtEOF(t *testing.T) {
	for _, bufSize := range []int{1, 1024} {
		input := []byte("done\x10")
		out, detached := drainDetachKeyReader(
			t,
			newDetachKeyReader(bytes.NewReader(input), []byte{0x10, 0x11}),
			bufSize,
		)
		assert.Equal(t, out, "done\x10", "bufSize=%d", bufSize)
		assert.Assert(t, !detached, "bufSize=%d", bufSize)
	}
}

// TestDetachKeyReader_ReturnsAvailableWithoutBlocking guards the io.Reader
// contract against a blocking source (a real pipe/terminal, unlike the
// bytes.Reader the other tests use): a single byte must come back promptly
// rather than stalling while the reader tries to fill the whole buffer.
func TestDetachKeyReader_ReturnsAvailableWithoutBlocking(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })
	r := newDetachKeyReader(pr, []byte{0x10, 0x11})

	go func() { _, _ = pw.Write([]byte("r")) }()

	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, 32*1024)
		n, err := r.Read(buf)
		done <- result{n, err}
	}()

	select {
	case got := <-done:
		assert.NilError(t, got.err)
		assert.Equal(t, got.n, 1)
	case <-time.After(2 * time.Second):
		t.Fatal("Read blocked instead of returning the available byte")
	}
}

func TestDetachKeyReader_EmptyKeysIsPassthrough(t *testing.T) {
	input := []byte("\x10\x11anything")
	r := newDetachKeyReader(bytes.NewReader(input), nil)

	data, err := io.ReadAll(r)
	assert.NilError(t, err)
	assert.Equal(t, string(data), string(input))
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
