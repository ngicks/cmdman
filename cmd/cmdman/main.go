package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/ngicks/cmdman/cmd/cmdman/commands"
	"github.com/ngicks/cmdman/cmd/internal/cmdsignals"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		cmdsignals.ExitSignals[:]...,
	)
	defer stop()

	err := commands.Execute(ctx)
	if err == nil {
		return
	}
	if !errors.Is(err, cli.ErrForceExit) {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(1)
}
