package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeConfigCmd(parent *cobra.Command, cf *composeFlags) {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Resolve and render the compose file in canonical format",
		Long: "Load the compose file, apply interpolation and env/path resolution, " +
			"validate it, and print the resulting project as a canonical, fully " +
			"resolved compose YAML document. The output is itself a valid compose " +
			"file; feeding it back to cmdman yields the same plan.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeConfig(cmd, cf)
		},
	}

	parent.AddCommand(cmd)
}

func runComposeConfig(cmd *cobra.Command, cf *composeFlags) error {
	spec, err := compose.LoadAndNormalize(cf.normalizeOpts())
	if err != nil {
		return err
	}
	return cli.RenderComposeConfig(cmd.OutOrStdout(), compose.Canonicalize(spec))
}
