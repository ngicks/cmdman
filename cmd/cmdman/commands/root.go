// Package commands implements the cmdman CLI subcommands. It composes the
// root cobra.Command, wires every leaf subcommand via its wrapper function,
// and translates parsed flags / positional arguments into calls on the
// service in pkg/cmdman.
package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/internal/loggerfactory"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

func Execute(ctx context.Context) error {
	return rootCmd().ExecuteContext(ctx)
}

func rootCmd() *cobra.Command {
	var (
		logConfig   *loggerfactory.Config
		rootConfig  cmdman.CmdmanConfig
		flagVersion bool
	)

	cmd := &cobra.Command{
		Use:   "cmdman",
		Short: "command manager",
		Long: `cmdman, the command manager, is a simple command daemon.
It's the podman without pods, or the tmux without terminals.
It simply starts a monitor process and the monitor damonizes itself and starts specified commands.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if err := loggerfactory.ReadEnv(logConfig, "cmdman", os.Environ()); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
			logger := loggerfactory.BuildLogger(logConfig)
			slog.SetDefault(logger)
			cmd.SetContext(contextkey.WithSlogLogger(cmd.Context(), logger))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if flagVersion {
				return runVersion(cmd, args)
			}
			return runRoot(cmd, args)
		},
	}

	logConfig = loggerfactory.RegisterFlags(cmd)
	cmd.Flags().BoolVar(&flagVersion, "version", false, "alias for the version subcommand")

	flags := cmd.PersistentFlags()
	flags.StringVar(&rootConfig.DataDir, "data-dir", "", "Cmdman data directory")
	flags.StringVar(&rootConfig.RuntimeDir, "runtime-dir", "", "Cmdman runtime directory")

	versionCmd(cmd)

	attachCmd(cmd, &rootConfig)
	createCmd(cmd, &rootConfig)
	eventsCmd(cmd, &rootConfig)
	inspectCmd(cmd, &rootConfig)
	logsCmd(cmd, &rootConfig)
	lsCmd(cmd, &rootConfig)
	migrateCmd(cmd, &rootConfig)
	monitorCmd(cmd, &rootConfig)
	restartCmd(cmd, &rootConfig)
	rmCmd(cmd, &rootConfig)
	runCmd(cmd, &rootConfig)
	sendKeysCmd(cmd, &rootConfig)
	signalCmd(cmd, &rootConfig)
	startCmd(cmd, &rootConfig)
	stopCmd(cmd, &rootConfig)
	waitCmd(cmd, &rootConfig)

	composeCmd(cmd, &rootConfig)

	return cmd
}

func runRoot(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}
