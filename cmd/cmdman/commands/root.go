// Package commands implements the cmdman CLI subcommands. It composes the
// root cobra.Command, wires every leaf subcommand via its wrapper function,
// and translates parsed flags / positional arguments into calls on the
// service in cmdman.
package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ngicks/go-common/contextkey"
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/internal/loggerfactory"
)

func Execute(ctx context.Context) error {
	return rootCmd().ExecuteContext(ctx)
}

// rootFlags carries the root command's persistent, config-affecting flags. They
// are bound to these fields instead of into a cmdman.CmdmanConfig so that a flag
// default cannot clobber the config file or the environment; cmdmanService
// overlays the explicitly-set ones onto the loaded config (see loadConfig).
type rootFlags struct {
	config     string
	dataDir    string
	runtimeDir string
}

func rootCmd() *cobra.Command {
	var (
		logConfig   *loggerfactory.Config
		rf          rootFlags
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
	flags.StringVar(&rf.config, "config", "",
		"Config file path; overrides $"+cmdman.ENV_CMDMAN_CONF+" and the default location")
	flags.StringVar(&rf.dataDir, "data-dir", "", "Cmdman data directory")
	flags.StringVar(&rf.runtimeDir, "runtime-dir", "", "Cmdman runtime directory")

	versionCmd(cmd)
	configCmd(cmd, &rf)

	attachCmd(cmd, &rf)
	createCmd(cmd, &rf)
	eventsCmd(cmd, &rf)
	inspectCmd(cmd, &rf)
	logsCmd(cmd, &rf)
	lsCmd(cmd, &rf)
	migrateCmd(cmd, &rf)
	monitorCmd(cmd, &rf)
	muxCmd(cmd, &rf)
	restartCmd(cmd, &rf)
	rmCmd(cmd, &rf)
	runCmd(cmd, &rf)
	sendKeysCmd(cmd, &rf)
	signalCmd(cmd, &rf)
	startCmd(cmd, &rf)
	statusCmd(cmd, &rf)
	stopCmd(cmd, &rf)
	tuiCmd(cmd, &rf)
	waitCmd(cmd, &rf)

	composeCmd(cmd, &rf)

	return cmd
}

func runRoot(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}
