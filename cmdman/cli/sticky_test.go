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

	"github.com/ngicks/go-common/iopipe"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/cli"
)

// stickyTestTimeout is a deadlock guard for the asynchronous sticky-wait tests,
// not a performance budget. In the success case PromptStickyWait/AttachSticky
// react to a keypress or ctx cancellation within milliseconds; this deadline
// only exists to fail a genuine hang instead of blocking the whole package.
//
// It is generous on purpose. These tests used to flake at the old 2s/3s values
// because of a real startup race in a hand-rolled stdin multiplexer sticky.go
// once had: it pumped and dropped the first keystroke before a consumer was
// registered, so the read hung forever. sticky.go now derives per-consumer
// stdin pipes from an iopipe.Reader, which reads the source only while a
// derived pipe is active and carries undelivered bytes over between pipes,
// making the success path deterministic and fast. The wide margin keeps this
// guard from firing on scheduler latency under a full parallel `go test ./...`
// run while still catching a future hang regression before the package timeout.
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

// attachPipes wires src and dst into running iopipe controllers for building
// cli.AttachOptions; their forwarding goroutines stop at test cleanup.
func attachPipes(t *testing.T, src io.Reader, dst io.Writer) (*iopipe.Reader, *iopipe.Writer) {
	t.Helper()
	r := iopipe.NewReader(src)
	w := iopipe.NewWriter(dst)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Run(ctx)
	go w.Run(ctx)
	return r, w
}

// syncBuffer is a goroutine-safe bytes.Buffer used as the display sink behind
// a stdout controller: the controller's forwarding goroutine writes it while
// the test goroutine reads it back.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

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

// TestAttachStickyReattachKeepsStdout verifies that output from attach
// iterations AFTER the first still reaches the display sink. Each Attach run
// derives its own pipe from the shared stdout controller and closes only that
// pipe on exit, so the sink must keep receiving across a close/derive cycle.
// The regression it guards: the command restarts and re-attaches, but its
// output is silently dropped — the mux pane shows nothing ("restart but no
// reattach").
func TestAttachStickyReattachKeepsStdout(t *testing.T) {
	stdin, stdout := nonTTYStdio(t)
	stdinPipeR, stdinPipeW := io.Pipe()
	t.Cleanup(func() { _ = stdinPipeW.Close(); _ = stdinPipeR.Close() })

	sink := &syncBuffer{}
	in, out := attachPipes(t, stdinPipeR, sink)

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
		StdinPipe:  in,
		StdoutPipe: out,
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

	got := sink.String()
	assert.Assert(t, strings.Contains(got, "chunk-1;"),
		"first-iteration output missing; got %q", got)
	assert.Assert(t, strings.Contains(got, "chunk-2;"),
		"reattach output dropped after re-derive of the stdout pipe; got %q", got)
}

// TestAttachStickyStdinEOFDetaches verifies that an exhausted stdin source
// detaches the sticky loop instead of leaving it stuck at a wait prompt nobody
// can answer. The hand-rolled multiplexer this loop used before iopipe never
// surfaced source EOF to the prompt reader, so the loop hung until ctx cancel.
func TestAttachStickyStdinEOFDetaches(t *testing.T) {
	t.Parallel()

	stdin, stdout := nonTTYStdio(t)
	sink := &syncBuffer{}
	in, out := attachPipes(t, bytes.NewReader(nil), sink) // stdin EOFs immediately

	var stateCalls atomic.Int64
	hooks := cli.StickyHooks{
		State: func(context.Context) (cli.StickyState, error) {
			if stateCalls.Add(1) == 1 {
				return cli.StickyState{Running: true, Status: "running"}, nil
			}
			return cli.StickyState{Running: false, Status: "exited (code 0)"}, nil
		},
		OpenSession: func(context.Context) (cli.AttachSession, error) {
			return newScriptedSession(false, []byte("out;")), nil
		},
		Restart: func(context.Context) error { return nil },
	}
	opts := cli.AttachOptions{
		Stdin:      stdin,
		Stdout:     stdout,
		StdinPipe:  in,
		StdoutPipe: out,
		DetachKeys: "ctrl-p,ctrl-q",
	}

	done := make(chan error, 1)
	go func() { done <- cli.AttachSticky(t.Context(), hooks, opts) }()

	select {
	case err := <-done:
		assert.NilError(t, err)
	case <-time.After(stickyTestTimeout):
		t.Fatal("AttachSticky hung on exhausted stdin")
	}
	assert.Assert(t, strings.Contains(sink.String(), "out;"),
		"attach output before stdin EOF detach missing; got %q", sink.String())
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
	in, out := attachPipes(t, stdinPipeR, &syncBuffer{})

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
		StdinPipe:  in,
		StdoutPipe: out,
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
	in, out := attachPipes(t, pr, &syncBuffer{})

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
				StdinPipe:  in,
				StdoutPipe: out,
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
	in, out := attachPipes(t, pr, &syncBuffer{})

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
				StdinPipe:  in,
				StdoutPipe: out,
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
	in, out := attachPipes(t, pr, &syncBuffer{})

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
				StdinPipe:  in,
				StdoutPipe: out,
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
