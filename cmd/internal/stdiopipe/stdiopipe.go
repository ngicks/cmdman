// Package stdiopipe provides a cancellable reader backed by os.Stdin.
package stdiopipe

import (
	"context"
	"io"
	"os"
	"sync"
)

var (
	onceStdin  sync.Once
	onceStdout sync.Once
	onceStderr sync.Once
)

// Stdin returns an [io.ReadCloser] which is piped to [os.Stdin] through an [io.Pipe].
//
// This is necessary because Read calls on [os.Stdin] cannot be unblocked by closing it.
//
// Only one invocation is allowed per process; a second call will panic.
func Stdin(ctx context.Context) io.ReadCloser {
	var pr *io.PipeReader
	called := false
	onceStdin.Do(func() {
		called = true
		var pw *io.PipeWriter
		pr, pw = io.Pipe()
		go func() {
			<-ctx.Done()
			pr.CloseWithError(ctx.Err())
		}()
		go func() {
			_, err := io.Copy(pw, os.Stdin)
			pw.CloseWithError(err)
		}()
	})
	if !called {
		panic("stdiopipe: Stdin is called more than once")
	}
	return pr
}

func stdOutput(ctx context.Context, label string, out *os.File, once *sync.Once) io.WriteCloser {
	var wc io.WriteCloser
	called := false
	once.Do(func() {
		called = true
		var pr *io.PipeReader
		var pw *io.PipeWriter
		pr, pw = io.Pipe()
		done := make(chan struct{})
		go func() {
			<-ctx.Done()
			pw.CloseWithError(ctx.Err())
		}()
		go func() {
			defer close(done)
			_, err := io.Copy(out, pr)
			pr.CloseWithError(err)
		}()
		wc = &stdoutWriteCloser{
			ctx:  ctx,
			pw:   pw,
			done: done,
		}
	})
	if !called {
		panic("stdiopipe: " + label + " is called more than once")
	}
	return wc
}

type stdoutWriteCloser struct {
	ctx  context.Context
	pw   *io.PipeWriter
	done <-chan struct{}
}

func (w *stdoutWriteCloser) Write(p []byte) (int, error) {
	return w.pw.Write(p)
}

func (w *stdoutWriteCloser) Close() error {
	err := w.pw.Close()
	select {
	case <-w.done:
		return err
	case <-w.ctx.Done():
		if err != nil {
			return err
		}
		return w.ctx.Err()
	}
}

// Stdout returns an [io.WriteCloser] which is piped to [os.Stdout] through an [io.Pipe].
//
// This is necessary because Write calls on [os.Stdout] cannot be unblocked by closing it.
//
// Only one invocation is allowed per process; a second call will panic.
func Stdout(ctx context.Context) io.WriteCloser {
	return stdOutput(ctx, "Stdout", os.Stdout, &onceStdout)
}

// Stderr returns an [io.WriteCloser] which is piped to [os.Stderr] through an [io.Pipe].
//
// This is necessary because Write calls on [os.Stderr] cannot be unblocked by closing it.
//
// Only one invocation is allowed per process; a second call will panic.
func Stderr(ctx context.Context) io.WriteCloser {
	return stdOutput(ctx, "Stderr", os.Stderr, &onceStderr)
}
