package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	"github.com/ngicks/cmdman/cmd/cmdman/commands"
	cmdsignals "github.com/ngicks/cmdman/cmd/internal/signals"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), cmdsignals.ExitSignals[:]...)
	defer stop()

	logger := slog.New(
		slog.NewJSONHandler(
			os.Stderr,
			&slog.HandlerOptions{
				AddSource: true,
				Level:     slog.LevelDebug,
			},
		),
	)
	ctx = contextkey.WithSlogLogger(ctx, logger)

	if err := commands.Execute(ctx, cmdman.CmdmanConfig{}); err != nil {
		os.Exit(1)
	}
}
