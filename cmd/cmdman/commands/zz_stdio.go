package commands

import (
	"context"
	"io"
	"os"

	"github.com/ngicks/go-common/iopipe"
)

// The stdio helpers below front os.Stdin / os.Stdout / os.Stderr with an
// iopipe controller so a subcommand blocked in Read/Write unblocks when its
// context is done. Each returns the derived pipe plus a stop func that closes
// it and stops forwarding; the underlying file is never closed. The per-pipe
// closeErr channel is dropped: the CLI has no use for the delivered-byte
// report, and its buffer keeps iopipe from blocking on it.
//
// Callers own the returned pipe's lifetime and must call stop even when the
// cli layer also closes the pipe - closing twice is a no-op.

// stdinPipe returns a cancellable read end of os.Stdin.
//
// A Read already blocked in os.Stdin cannot be interrupted, so the forwarding
// goroutine can outlive stop; it is reclaimed when the process exits.
func stdinPipe(ctx context.Context) (io.ReadCloser, func(), error) {
	r := iopipe.NewReader(os.Stdin)
	runCtx, cancel := context.WithCancel(ctx)
	rc, _, err := r.Pipe(runCtx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	go r.Run(runCtx)
	return rc, func() {
		_ = rc.Close()
		cancel()
	}, nil
}

// stdoutPipe returns a cancellable write end of os.Stdout.
func stdoutPipe(ctx context.Context) (io.WriteCloser, func(), error) {
	return writerPipe(ctx, os.Stdout)
}

// stderrPipe returns a cancellable write end of os.Stderr.
func stderrPipe(ctx context.Context) (io.WriteCloser, func(), error) {
	return writerPipe(ctx, os.Stderr)
}

func writerPipe(ctx context.Context, dst io.Writer) (io.WriteCloser, func(), error) {
	w := iopipe.NewWriter(dst)
	runCtx, cancel := context.WithCancel(ctx)
	wc, _, err := w.Pipe(runCtx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	go w.Run(runCtx)
	return wc, func() {
		_ = wc.Close()
		cancel()
	}, nil
}
