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
