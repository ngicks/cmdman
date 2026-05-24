package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// composeFlags holds the common flags shared across all compose subcommands.
type composeFlags struct {
	File        string
	ProjectName string
	WorkDir     string
}

// normalizeOpts converts the parsed CLI flags into the service-layer options.
func (cf *composeFlags) normalizeOpts() compose.NormalizeOpts {
	return compose.NormalizeOpts{
		File:        cf.File,
		ProjectName: cf.ProjectName,
		WorkDir:     cf.WorkDir,
	}
}

func composeCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var (
		flags composeFlags
	)

	cmd := &cobra.Command{
		Use:   "compose",
		Short: "Manage groups of commands defined in a compose file",
	}

	pf := cmd.PersistentFlags()
	pf.StringVarP(
		&flags.File, "file", "f", "",
		"Compose file path (default: cmd-compose.yaml or cmd-compose.yml in CWD)",
	)
	pf.StringVarP(
		&flags.ProjectName,
		"project-name",
		"p",
		"",
		"Project name (overrides YAML name:)",
	)
	pf.StringVar(&flags.WorkDir, "workdir", "", "Override the effective work directory")

	composeCreateCmd(cmd, rootCfg, &flags)
	composeUpCmd(cmd, rootCfg, &flags)
	composeStartCmd(cmd, rootCfg, &flags)
	composeStopCmd(cmd, rootCfg, &flags)
	composeRestartCmd(cmd, rootCfg, &flags)
	composeDownCmd(cmd, rootCfg, &flags)
	composeLogsCmd(cmd, rootCfg, &flags)
	composeSignalCmd(cmd, rootCfg, &flags)
	composeWaitCmd(cmd, rootCfg, &flags)

	parent.AddCommand(cmd)
}
