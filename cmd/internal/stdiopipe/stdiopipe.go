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

func stdout(ctx context.Context, label string, out *os.File, once *sync.Once) io.WriteCloser {
	var pw *io.PipeWriter
	called := false
	once.Do(func() {
		called = true
		var pr *io.PipeReader
		pr, pw = io.Pipe()
		go func() {
			<-ctx.Done()
			pw.CloseWithError(ctx.Err())
		}()
		go func() {
			_, err := io.Copy(out, pr)
			pr.CloseWithError(err)
		}()
	})
	if !called {
		panic("stdiopipe: " + label + " is called more than once")
	}
	return pw
}

// Stdout returns an [io.WriteCloser] which is piped to [os.Stdout] through an [io.Pipe].
//
// This is necessary because Write calls on [os.Stdout] cannot be unblocked by closing it.
//
// Only one invocation is allowed per process; a second call will panic.
func Stdout(ctx context.Context) io.WriteCloser {
	return stdout(ctx, "Stdout", os.Stdout, &onceStdout)
}

// Stderr returns an [io.WriteCloser] which is piped to [os.Stderr] through an [io.Pipe].
//
// This is necessary because Write calls on [os.Stderr] cannot be unblocked by closing it.
//
// Only one invocation is allowed per process; a second call will panic.
func Stderr(ctx context.Context) io.WriteCloser {
	return stdout(ctx, "Stderr", os.Stderr, &onceStderr)
}
