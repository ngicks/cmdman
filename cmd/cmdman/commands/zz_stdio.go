package commands

import (
	"context"
	"io"
	"os"

	"github.com/ngicks/go-common/iopipe"
)

// The stdio helpers below front os.Stdin / os.Stdout / os.Stderr with an
// iopipe controller so a subcommand blocked in Read/Write unblocks when its
// context is done. The underlying file is never closed, and the per-pipe
// closeErr channel is dropped: the CLI has no use for the delivered-byte
// report, and its buffer keeps iopipe from blocking on it.
//
// attachStdio hands cli.Attach* the controllers themselves (the cli layer
// derives one pipe per attach run / wait prompt); writerPipe and friends
// return a single derived pipe for commands that stream into one consumer.

// attachStdio returns iopipe controllers fronting os.Stdin and os.Stdout,
// running, for cli.AttachOptions. The stop func stops forwarding; a Read
// already blocked in os.Stdin cannot be interrupted, so the stdin forwarding
// goroutine can outlive stop and is reclaimed when the process exits.
func attachStdio(ctx context.Context) (stdin *iopipe.Reader, stdout *iopipe.Writer, stop func()) {
	r := iopipe.NewReader(os.Stdin)
	w := iopipe.NewWriter(os.Stdout)
	runCtx, cancel := context.WithCancel(ctx)
	go r.Run(runCtx)
	go w.Run(runCtx)
	return r, w, cancel
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
