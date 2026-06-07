package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

// stickyTestTimeout is a deadlock guard for the asynchronous sticky-wait tests,
// not a performance budget. In the success case PromptStickyWait/AttachSticky
// react to a keypress or ctx cancellation within milliseconds; this deadline
// only exists to fail a genuine hang instead of blocking the whole package.
//
// It is generous on purpose. These tests used to flake at the old 2s/3s values
// because of a real startup race in stdinMux: it pumped and dropped the first
// keystroke before a consumer was registered, so the read hung forever. That is
// fixed in sticky.go (the pump now starts only once a consumer exists), making
// the success path deterministic and fast. The wide margin keeps this guard
// from firing on scheduler latency under a full parallel `go test ./...` run
// while still catching a future hang regression before the package test timeout.
const stickyTestTimeout = 30 * time.Second

// nonTTYStdio returns an (stdin, stdout) pair of *os.File handles that
// behave like real files but are NOT TTYs, so setupRawTerminal is a no-op.
// Callers MUST close both at end of test.
func nonTTYStdio(t *testing.T) (stdin, stdout *os.File) {
	t.Helper()
	stdinR, stdinW, err := os.Pipe()
	assert.NilError(t, err)
	t.Cleanup(func() { _ = stdinW.Close(); _ = stdinR.Close() })
	stdoutR, stdoutW, err := os.Pipe()
	assert.NilError(t, err)
	t.Cleanup(func() { _ = stdoutW.Close(); _ = stdoutR.Close() })
	return stdinR, stdoutW
}

// bufWriteCloser is an io.WriteCloser backed by a bytes.Buffer; Close is a
// no-op. Useful as a non-cancelling StdoutPipe in tests.
type bufWriteCloser struct {
	*bytes.Buffer
}

func (*bufWriteCloser) Close() error { return nil }

// fakeAttachSession is a cli.AttachSession whose Recv immediately returns a
// configured error, standing in for a monitor stream that breaks mid-attach.
type fakeAttachSession struct {
	recvErr error
}

func (f *fakeAttachSession) Recv() ([]byte, error)               { return nil, f.recvErr }
func (f *fakeAttachSession) SendStdin([]byte) error              { return nil }
func (f *fakeAttachSession) Signal(context.Context, int32) error { return nil }
func (f *fakeAttachSession) Resize(int, int) error               { return nil }
func (f *fakeAttachSession) CloseSend() error                    { return nil }
func (f *fakeAttachSession) Close() error                        { return nil }

// scriptedSession is a cli.AttachSession that yields the bytes in chunks once
// (one per Recv call) and then behaves like a command that exited: it returns
// io.EOF, unless blockUntilClose is set, in which case Recv blocks until Close
// is called (standing in for a still-running command being torn down).
type scriptedSession struct {
	chunks          [][]byte
	idx             int
	blockUntilClose bool
	closed          chan struct{}
	closeOnce       sync.Once
}

func newScriptedSession(blockUntilClose bool, chunks ...[]byte) *scriptedSession {
	return &scriptedSession{
		chunks:          chunks,
		blockUntilClose: blockUntilClose,
		closed:          make(chan struct{}),
	}
}

func (s *scriptedSession) Recv() ([]byte, error) {
	if s.idx < len(s.chunks) {
		c := s.chunks[s.idx]
		s.idx++
		return c, nil
	}
	if s.blockUntilClose {
		<-s.closed
	}
	return nil, io.EOF
}

func (s *scriptedSession) SendStdin([]byte) error              { return nil }
func (s *scriptedSession) Signal(context.Context, int32) error { return nil }
func (s *scriptedSession) Resize(int, int) error               { return nil }
func (s *scriptedSession) CloseSend() error                    { return nil }
func (s *scriptedSession) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

// pipeWriteCloser drains an io.Pipe into a mutex-guarded buffer, mirroring the
// real stdiopipe.Stdout: Write goes through the pipe, and Close() actually
// closes the pipe (so writes after Close fail and the drain goroutine exits).
// A plain bytes.Buffer with a no-op Close would mask the reattach bug because
// its writes never fail.
type pipeWriteCloser struct {
	pw   *io.PipeWriter
	mu   *sync.Mutex
	buf  *bytes.Buffer
	done chan struct{}
}

func newPipeWriteCloser() *pipeWriteCloser {
	pr, pw := io.Pipe()
	w := &pipeWriteCloser{
		pw:   pw,
		mu:   &sync.Mutex{},
		buf:  &bytes.Buffer{},
		done: make(chan struct{}),
	}
	go func() {
		defer close(w.done)
		b := make([]byte, 4096)
		for {
			n, err := pr.Read(b)
			if n > 0 {
				w.mu.Lock()
				w.buf.Write(b[:n])
				w.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return w
}

func (w *pipeWriteCloser) Write(p []byte) (int, error) { return w.pw.Write(p) }
func (w *pipeWriteCloser) Close() error                { return w.pw.Close() }

func (w *pipeWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// TestAttachStickyReattachKeepsStdout verifies that output from attach
// iterations AFTER the first still reaches the display sink. Attach closes the
// StdoutPipe it is handed on every exit; AttachSticky reuses one display sink
// across iterations, so it must shield that sink from Attach's close. The
// regression it guards: the command restarts and re-attaches, but its output
// is written into a pipe Attach already closed and silently dropped — the mux
// pane shows nothing ("restart but no reattach").
func TestAttachStickyReattachKeepsStdout(t *testing.T) {
	stdin, stdout := nonTTYStdio(t)
	stdinPipeR, stdinPipeW := io.Pipe()
	t.Cleanup(func() { _ = stdinPipeW.Close(); _ = stdinPipeR.Close() })

	sink := newPipeWriteCloser()

	// State stays Running so the loop re-opens a session on each EOF without
	// going through the wait prompt: the exact multi-iteration stdout reuse
	// the bug lives in, minus the stdin timing of a 'r' keypress.
	var openCount atomic.Int64
	reached := make(chan struct{})
	hooks := cli.StickyHooks{
		State: func(context.Context) (cli.StickyState, error) {
			return cli.StickyState{Running: true, Status: "running"}, nil
		},
		OpenSession: func(context.Context) (cli.AttachSession, error) {
			k := openCount.Add(1)
			switch k {
			case 1:
				return newScriptedSession(false, []byte("chunk-1;")), nil
			case 2:
				return newScriptedSession(false, []byte("chunk-2;")), nil
			default:
				// Third open: both prior iterations have delivered their
				// output and EOF'd. Block here until ctx cancel tears it down.
				close(reached)
				return newScriptedSession(true), nil
			}
		},
		Restart: func(context.Context) error { return nil },
	}
	opts := cli.AttachOptions{
		Stdin:      stdin,
		Stdout:     stdout,
		StdinPipe:  stdinPipeR,
		StdoutPipe: sink,
		DetachKeys: "ctrl-p,ctrl-q",
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cli.AttachSticky(ctx, hooks, opts) }()

	select {
	case <-reached:
	case <-time.After(stickyTestTimeout):
		cancel()
		t.Fatal("AttachSticky did not reach the third attach iteration")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(stickyTestTimeout):
		t.Fatal("AttachSticky did not return after ctx cancel")
	}

	// Close the sink so its drain goroutine flushes and exits, then read it.
	_ = sink.Close()
	<-sink.done

	got := sink.String()
	assert.Assert(t, strings.Contains(got, "chunk-1;"),
		"first-iteration output missing; got %q", got)
	assert.Assert(t, strings.Contains(got, "chunk-2;"),
		"reattach output dropped — Attach closed the shared StdoutPipe; got %q", got)
}

// TestAttachStickyRecoverableAttachErrorDropsToPrompt verifies that a non-EOF
// attach error (e.g. the monitor tearing the stream down on stop/restart) does
// NOT propagate out of AttachSticky — which would kill the viewer and close
// its mux pane. Instead the loop drops to the wait prompt; here ctx
// cancellation stands in for the user detaching, so the call returns the ctx
// error rather than the raw stream error.
func TestAttachStickyRecoverableAttachErrorDropsToPrompt(t *testing.T) {
	stdin, stdout := nonTTYStdio(t)
	stdinPipeR, stdinPipeW := io.Pipe()
	t.Cleanup(func() { _ = stdinPipeW.Close(); _ = stdinPipeR.Close() })

	errBoom := errors.New("rpc error: transport is closing")
	hooks := cli.StickyHooks{
		State: func(context.Context) (cli.StickyState, error) {
			return cli.StickyState{Running: true, Status: "running"}, nil
		},
		OpenSession: func(context.Context) (cli.AttachSession, error) {
			return &fakeAttachSession{recvErr: errBoom}, nil
		},
		Restart: func(context.Context) error { return nil },
	}
	opts := cli.AttachOptions{
		Stdin:      stdin,
		Stdout:     stdout,
		StdinPipe:  stdinPipeR,
		StdoutPipe: &bufWriteCloser{Buffer: &bytes.Buffer{}},
		DetachKeys: "ctrl-p,ctrl-q",
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cli.AttachSticky(ctx, hooks, opts) }()

	// Give the loop time to reach Attach (which errors) and drop to the prompt.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		assert.Assert(t, !errors.Is(err, errBoom),
			"raw attach error must not propagate; got %v", err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(stickyTestTimeout):
		t.Fatal("AttachSticky did not return after ctx cancel")
	}
}

func TestPromptStickyWaitR(t *testing.T) {
	t.Parallel()

	stdin, stdout := nonTTYStdio(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	resultCh := make(chan cli.PromptResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := cli.PromptStickyWait(
			t.Context(),
			"exited (code 0)",
			cli.AttachOptions{
				DetachKeys: "ctrl-p,ctrl-q",
				Stdin:      stdin,
				Stdout:     stdout,
				StdinPipe:  pr,
				StdoutPipe: &bufWriteCloser{Buffer: &bytes.Buffer{}},
			},
		)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- res
	}()

	_, _ = pw.Write([]byte("r"))
	select {
	case res := <-resultCh:
		assert.Equal(t, res, cli.PromptRestart)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(stickyTestTimeout):
		t.Fatal("PromptStickyWait did not return")
	}
}

func TestPromptStickyWaitDetachKeys(t *testing.T) {
	t.Parallel()

	stdin, stdout := nonTTYStdio(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	resultCh := make(chan cli.PromptResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := cli.PromptStickyWait(
			t.Context(),
			"created",
			cli.AttachOptions{
				DetachKeys: "ctrl-p,ctrl-q",
				Stdin:      stdin,
				Stdout:     stdout,
				StdinPipe:  pr,
				StdoutPipe: &bufWriteCloser{Buffer: &bytes.Buffer{}},
			},
		)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- res
	}()

	// ctrl-p (0x10) ctrl-q (0x11).
	_, _ = pw.Write([]byte{0x10, 0x11})
	select {
	case res := <-resultCh:
		assert.Equal(t, res, cli.PromptDetach)
	case err := <-errCh:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(stickyTestTimeout):
		t.Fatal("PromptStickyWait did not return")
	}
}

func TestPromptStickyWaitContextCancel(t *testing.T) {
	t.Parallel()

	stdin, stdout := nonTTYStdio(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	ctx, cancel := context.WithCancel(t.Context())

	resultCh := make(chan cli.PromptResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := cli.PromptStickyWait(
			ctx,
			"created",
			cli.AttachOptions{
				DetachKeys: "ctrl-p,ctrl-q",
				Stdin:      stdin,
				Stdout:     stdout,
				StdinPipe:  pr,
				StdoutPipe: &bufWriteCloser{Buffer: &bytes.Buffer{}},
			},
		)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- res
	}()

	cancel()
	select {
	case <-resultCh:
		t.Fatal("expected ctx error, got result")
	case err := <-errCh:
		assert.Assert(t, errors.Is(err, context.Canceled))
	case <-time.After(stickyTestTimeout):
		t.Fatal("PromptStickyWait did not return on ctx cancel")
	}
}
