package commands

import (
	"context"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/spf13/cobra"
)

// Execute runs the root command with the given context.
func Execute(ctx context.Context, cfg cmdman.CmdmanConfig) error {
	rootConfig = cfg
	return rootCmd.ExecuteContext(ctx)
}

var rootConfig cmdman.CmdmanConfig

var rootCmd = &cobra.Command{
	Use:   "cmdman",
	Short: "command manager",
	Long: `cmdman, the command manager, is a simple command daemon.
It's the podman without pods, or the tmux without terminals.
It simply starts a monitor process and the monitor damonizes itself and starts specified commands.`,
	SilenceUsage: true,
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVar(&rootConfig.DataDir, "data-dir", "", "Cmdman data directory")
	flags.StringVar(&rootConfig.RuntimeDir, "runtime-dir", "", "Cmdman runtime directory")
}

func cmdmanConfig() (cmdman.CmdmanConfig, error) {
	return rootConfig.WithDefaults()
}

func cmdmanService() (*cmdman.Service, error) {
	cfg, err := cmdmanConfig()
	if err != nil {
		return nil, err
	}
	return cmdman.NewService(cfg), nil
}
