package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ngicks/cmdman/cmd/cmdman/commands"
	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/internal/cmdsignals"

	// Register the tmux driver for muxctl lookup.
	_ "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

func main() {
	blockOn, ctx, cancel := cmdsignals.NotifyContext(context.Background())

	// Run signal propagation alongside the command and always tear it down.
	var wg sync.WaitGroup
	wg.Go(blockOn)

	err := commands.Execute(ctx)

	// Match this context's error so an unrelated context.Canceled is not treated
	// as a signal; inspect the cause before cleanup cancellation changes ctx.Err().
	if err != nil && errors.Is(err, ctx.Err()) {
		if sigErr, ok := errors.AsType[*cmdsignals.SignalReceivedError](context.Cause(ctx)); ok {
			err = sigErr
		}
	}

	cancel(nil)
	wg.Wait()

	if err == nil {
		return
	}
	// ErrForceExit means the user hit the force-exit sequence during an attach;
	// the terminal is already restored and the message is intentionally silent.
	if !errors.Is(err, cli.ErrForceExit) {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(1)
}
