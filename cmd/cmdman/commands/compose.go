package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/compose"
)

type composeFlags struct {
	File        string
	ProjectName string
	WorkDir     string
}

func (cf *composeFlags) normalizeOpts() compose.NormalizeOpts {
	return compose.NormalizeOpts{
		File:        cf.File,
		ProjectName: cf.ProjectName,
		WorkDir:     cf.WorkDir,
	}
}

func composeCmd(parent *cobra.Command, rf *rootFlags) {
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
		"Compose file path, or a project name resolved under the compose/ subdir"+
			" of the cmdman config dir (the dir holding the config file, e.g."+
			" $XDG_CONFIG_HOME/cmdman); default: cmd-compose.yaml or"+
			" cmd-compose.yml in CWD",
	)
	pf.StringVarP(
		&flags.ProjectName,
		"project-name",
		"p",
		"",
		"Project name (overrides YAML name:)",
	)
	pf.StringVarP(&flags.WorkDir, "workdir", "w", "", "Override the effective work directory")
	_ = cmd.RegisterFlagCompletionFunc("file", completeComposeFile)

	composeCreateCmd(cmd, rf, &flags)
	composeLsCmd(cmd, rf)
	composeConfigCmd(cmd, &flags)
	composePsCmd(cmd, rf, &flags)
	composeStatusCmd(cmd, rf, &flags)
	composeInspectCmd(cmd, rf, &flags)
	composeUpCmd(cmd, rf, &flags)
	composeScaleCmd(cmd, rf, &flags)
	composeStartCmd(cmd, rf, &flags)
	composeStopCmd(cmd, rf, &flags)
	composeRestartCmd(cmd, rf, &flags)
	composeDownCmd(cmd, rf, &flags)
	composeLogsCmd(cmd, rf, &flags)
	composeEventsCmd(cmd, rf, &flags)
	composeSignalCmd(cmd, rf, &flags)
	composeWaitCmd(cmd, rf, &flags)
	composeAttachCmd(cmd, rf, &flags)
	composeSendKeysCmd(cmd, rf, &flags)
	composeMuxCmd(cmd, rf, &flags)

	parent.AddCommand(cmd)
}
