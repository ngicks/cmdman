package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"syscall"
	"testing"

	"github.com/moby/term"
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
		{"a", []byte{'a'}, false},
		{"a,b,c", []byte{'a', 'b', 'c'}, false},
		{"", nil, false},
		{"ctrl-", nil, true},
		{"ctrl-ab", nil, true},
		{"ctrl-1", nil, true},
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
		if _, ok := errors.AsType[term.EscapeError](err); ok {
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
		if _, ok := errors.AsType[term.EscapeError](err); ok {
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
	_, ok := errors.AsType[term.EscapeError](err)
	assert.Equal(t, n, 0)
	assert.Assert(t, ok)
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
	_, ok := errors.AsType[term.EscapeError](err)
	assert.Assert(t, ok)
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
