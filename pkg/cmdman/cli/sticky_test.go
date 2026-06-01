package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

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
			return cli.StickyState{Running: true, Status: "started"}, nil
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
	case <-time.After(3 * time.Second):
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
	case <-time.After(2 * time.Second):
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
	case <-time.After(2 * time.Second):
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
	case <-time.After(2 * time.Second):
		t.Fatal("PromptStickyWait did not return on ctx cancel")
	}
}
