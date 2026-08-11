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
	n, ctx, cancel := cmdsignals.NotifyContext(context.Background())

	var wg sync.WaitGroup
	wg.Go(n.Run)

	err := commands.Execute(ctx)

	// Check before cancel(nil) below — that call would set ctx.Err() and
	// manufacture a false positive.
	if err != nil && errors.Is(err, ctx.Err()) {
		if sigErr, ok := errors.AsType[*cmdsignals.SignalReceivedError](context.Cause(ctx)); ok {
			err = sigErr
		}
	}

	cancel(nil)
	n.Stop()
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
