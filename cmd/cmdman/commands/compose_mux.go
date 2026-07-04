package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func composeMuxCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "mux [layout]",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:" section
(alias of "compose mux up").

Each leaf references a compose service name; panes run cmdman attach by default,
or cmdman logs when mode: logs.

With no argument, mux cycles to the next layout each invocation (a fresh window
starts at the first layout). Pass a layout name or a 0-based index to apply that
layout directly; the choice becomes the new cycle position. A name is matched
before an index, so a layout literally named "2" wins over index 2.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).

Subcommands: up, down, ls, cycle-scale. A layout literally named "up", "down",
"ls", or "cycle-scale" must be passed as: cmdman compose mux up <name>.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeMuxUp(cmd, rootCfg, cf, args, flagSession)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	composeMuxUpCmd(cmd, rootCfg, cf, &flagSession)
	composeMuxDownCmd(cmd, cf, &flagSession)
	composeMuxLsCmd(cmd, rootCfg, cf, &flagSession)
	composeMuxCycleScaleCmd(cmd, rootCfg, cf, &flagSession)

	parent.AddCommand(cmd)
}

func composeMuxUpCmd(
	parent *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	parentSession *string,
) {
	var flagSession string

	cmd := &cobra.Command{
		Use:   "up [layout]",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:" section.

Each leaf references a compose service name; panes run cmdman attach by default,
or cmdman logs when mode: logs.

With no argument, mux cycles to the next layout each invocation (a fresh window
starts at the first layout). Pass a layout name or a 0-based index to apply that
layout directly; the choice becomes the new cycle position. A name is matched
before an index, so a layout literally named "2" wins over index 2.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxUp(cmd, rootCfg, cf, args, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)

	parent.AddCommand(cmd)
}

func composeMuxDownCmd(parent *cobra.Command, cf *composeFlags, parentSession *string) {
	var flagSession string

	cmd := &cobra.Command{
		Use:               "down",
		Short:             "Tear down the dashboard windows for this compose project",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: `Tear down the cmdman-owned dashboard windows matching this compose project.

The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The supervised commands keep
running — only the disposable viewers are torn down.

Window discovery is server-wide and requires no $TMUX context; it works from
any pane, run-shell, or outside tmux. --session narrows the scan to one session.

Down needs no cmdman service or leaf resolution — only the project identity
derived from the compose file is required.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxDown(cmd, cf, sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow teardown to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

func composeMuxLsCmd(
	parent *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
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
			return runComposeMuxLs(cmd, rootCfg, cf, sess, flagFormat)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow listing to this tmux session only (default: server-wide)",
	)
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.MuxLsFormatUsage())

	parent.AddCommand(cmd)
}

func composeMuxCycleScaleCmd(
	parent *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	parentSession *string,
) {
	var flagSession string

	cmd := &cobra.Command{
		Use:               "cycle-scale <command>[=N]",
		Short:             "Advance the replica position for a command in the compose mux dashboard",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeComposeMuxCycleScaleTargets(cf),
		Long: `Advance the replica shown for a compose service in the mux dashboard.

With no "=N" suffix, the command advances to the next replica (wrapping from
the last back to the first). With "=N" the pane jumps directly to replica N
(1-based). The new position persists in the dashboard window across layout
switches and is cleared by "compose mux down".

The target pane is located by the @cmdman_leaf option stamped on it by
"compose mux up". If the command is not visible in the current layout the
position is still updated; it will take effect on the next "compose mux up".

Only unpinned leaves (those without a "scale:" in the mux: section) are
cycle-scale targets. A leaf with an explicit "scale: N" is pinned and is never
advanced by this command.

Window discovery is server-wide and requires no $TMUX context. --session
narrows the operation to one session.

Note: a layout literally named "cycle-scale" must be passed as a layout
argument to "compose mux up".`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxCycleScale(cmd, rootCfg, cf, args[0], sess)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow operation to this tmux session only (default: server-wide)",
	)

	parent.AddCommand(cmd)
}

func runComposeMuxUp(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	args []string,
	session string,
) error {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	var layout string
	if len(args) > 0 {
		layout = args[0]
	}
	return compose.NewService(svc).MuxUp(cmd.Context(), compose.MuxUpOption{
		Selection:   selection,
		Layout:      layout,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

// runComposeMuxDown tears down the compose project's dashboard. It needs no
// cmdman service or leaf resolution — only the project identity and spec driver
// options are required. The layout argument is irrelevant to teardown.
func runComposeMuxDown(cmd *cobra.Command, cf *composeFlags, session string) error {
	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	// Down needs no cmdman service — only the project identity derived from the
	// compose file (see this command's Long help). Pass a nil service.
	return compose.NewService(nil).MuxDown(cmd.Context(), compose.MuxDownOption{
		Selection:   selection,
		SessionName: session,
		Stdout:      cmd.OutOrStdout(),
	})
}

func runComposeMuxLs(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
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
	svc, svcErr := cmdmanService(rootCfg)
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

// runComposeMuxCycleScale advances the replica position for a command across all
// matching dashboard windows.
func runComposeMuxCycleScale(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	arg, session string,
) error {
	// Parse <command>[=N]: split on "="; N must be >= 1 when present.
	command, posStr, hasPos := strings.Cut(arg, "=")
	if command == "" {
		return fmt.Errorf("cycle-scale: command name is empty in argument %q", arg)
	}
	var position int
	if hasPos {
		n, err := strconv.Atoi(posStr)
		if err != nil {
			return fmt.Errorf(
				"cycle-scale: invalid position %q in argument %q: not a number",
				posStr, arg,
			)
		}
		if n < 1 {
			return fmt.Errorf("cycle-scale: position must be >= 1, got %d", n)
		}
		position = n
	}

	selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
	if err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	result, cycleErr := compose.NewService(svc).MuxCycleScale(
		cmd.Context(),
		compose.MuxCycleScaleOption{
			Selection:   selection,
			SessionName: session,
			Command:     command,
			Position:    position,
		},
	)
	cli.RenderCycleScaleResult(cmd.OutOrStdout(), result)
	return cycleErr
}

// completeComposeMuxLayout completes the optional layout argument with the
// project's layout names, best-effort (a load failure yields no completions).
func completeComposeMuxLayout(cf *composeFlags) cobra.CompletionFunc {
	return func(
		cmd *cobra.Command,
		args []string,
		toComplete string,
	) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
		if err != nil || selection.Spec == nil || selection.Spec.Mux == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for _, l := range selection.Spec.Mux.Layouts {
			names = append(names, l.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeComposeMuxCycleScaleTargets completes the command argument with the
// spec's unpinned leaf command names (cycle-scale targets), best-effort.
func completeComposeMuxCycleScaleTargets(cf *composeFlags) cobra.CompletionFunc {
	return func(
		cmd *cobra.Command,
		args []string,
		toComplete string,
	) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		selection, err := compose.ResolveMuxSelection(cf.normalizeOpts())
		if err != nil || selection.Spec == nil || selection.Spec.Mux == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return mux.CollectCycleTargets(*selection.Spec.Mux), cobra.ShellCompDirectiveNoFileComp
	}
}
