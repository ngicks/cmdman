package commands

import (
	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman/cli"
	"github.com/ngicks/cmdman/cmdman/compose"
)

func composeMuxLsCmd(
	parent *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	parentSession *string,
) {
	var (
		flagSession string
		flagFormat  string
	)

	cmd := &cobra.Command{
		Use:               "ls",
		Short:             "List dashboard windows for this compose project",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: `List cmdman-owned dashboard windows for this compose project.

Discovery is server-wide and requires no $TMUX context; it works from any
pane, run-shell, or outside tmux. --session narrows the listing to one session.

Columns: SESSION, WINDOW, ID, IDENTITY, LAYOUT (-1 displayed as "-"), SCALE.
The SCALE column shows per-window cycle-target positions and live replica counts
(e.g. "web=2/3"). Counts are resolved from the cmdman store; when the store
has no entries the count renders as "?".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxLs(cmd, rf, cf, sess, flagFormat)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow listing to this tmux session only (default: server-wide)",
	)
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.MuxLsFormatUsage())

	parent.AddCommand(cmd)
}

func runComposeMuxLs(
	cmd *cobra.Command,
	rf *rootFlags,
	cf *composeFlags,
	session, format string,
) error {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	// The cmdman service is only needed for the best-effort live replica counts
	// in the SCALE column; a service-build failure must not block window listing.
	// On failure svc is nil, and NewService(nil) degrades the counts to "?".
	svc, svcErr := cmdmanService(cmd, rf)
	if svcErr == nil {
		defer svc.Close()
	}

	result, err := compose.NewService(svc).MuxLs(cmd.Context(), compose.MuxLsOption{
		Selection:   selection,
		SessionName: session,
	})
	if err != nil {
		return err
	}
	return cli.RenderMuxWindows(
		cmd.OutOrStdout(), result.Windows, result.ReplicaCounts, result.CycleTargets, format,
	)
}
