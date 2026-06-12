package commands

import (
	"errors"
	"fmt"
	"os"
	"slices"
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
	selection, err := resolveComposeMuxSelection(cf)
	if err != nil {
		return err
	}
	spec := *selection.Spec.Mux
	windowName := composeMuxWindowName(selection)

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	cfg := svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cmdman binary: %w", err)
	}

	composeSvc := compose.NewService(svc)
	resolver, replicas, err := composeSvc.MuxLeafResolver(cmd.Context(), selection)
	if err != nil {
		return err
	}

	scalePositions, err := mux.ReadScaleState(cmd.Context(), mux.ScaleStateOptions{
		Driver:      spec.Driver,
		DriverOpt:   spec.DriverOpt,
		SessionName: session,
		Identity:    selection.ProjectIdentity(),
	})
	if err != nil {
		return fmt.Errorf("read scale state: %w", err)
	}

	built, err := mux.Build(cmd.Context(), mux.BuildOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: replicas,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
		ScalePositions: scalePositions,
	})
	if err != nil {
		return err
	}

	var layout string
	if len(args) > 0 {
		layout = args[0]
	}
	return mux.Run(cmd.Context(), built, mux.RunOptions{
		SessionName: session,
		WindowName:  windowName,
		Identity:    selection.ProjectIdentity(),
		Layout:      layout,
		Stdout:      cmd.OutOrStdout(),
	})
}

// runComposeMuxDown tears down the compose project's dashboard. It needs no
// cmdman service or leaf resolution — only the project identity and spec driver
// options are required. The layout argument is irrelevant to teardown.
func runComposeMuxDown(cmd *cobra.Command, cf *composeFlags, session string) error {
	selection, err := resolveComposeMuxSelection(cf)
	if err != nil {
		return err
	}
	spec := *selection.Spec.Mux
	return mux.Down(cmd.Context(), mux.DownOptions{
		Driver:    spec.Driver,
		DriverOpt: spec.DriverOpt,
		// SessionName is a narrowing filter only; it is not used to derive the
		// identity. An explicit --session keeps the scan in one session.
		SessionName: session,
		Identity:    selection.ProjectIdentity(),
		// WindowName feeds identity derivation only when Identity is empty
		// (unnamed project): it keeps down aligned with what Run stamped
		// (the project-derived window name) instead of a session-derived
		// fallback.
		WindowName: composeMuxWindowName(selection),
		Stdout:     cmd.OutOrStdout(),
	})
}

func runComposeMuxLs(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	session, format string,
) error {
	selection, err := resolveComposeMuxSelection(cf)
	if err != nil {
		return err
	}
	spec := *selection.Spec.Mux

	// For an unnamed project (identity ""), fall back to the window name
	// ("cmdman") so the filter still matches what up stamped.
	identity := selection.ProjectIdentity()
	if identity == "" {
		identity = composeMuxWindowName(selection)
	}

	windows, err := mux.List(cmd.Context(), mux.ListOptions{
		Driver:      spec.Driver,
		DriverOpt:   spec.DriverOpt,
		SessionName: session,
		Identity:    identity,
	})
	if err != nil {
		return err
	}

	targets := collectCycleTargets(spec)

	// Resolve live replica counts for the SCALE column. Commands whose count
	// cannot be resolved (store unavailable, replica missing live) are left
	// absent from the map and render as "pos/?".
	replicaCounts := make(map[string]int, len(targets))
	if svc, svcErr := cmdmanService(rootCfg); svcErr == nil {
		defer svc.Close()
		if _, counter, counterErr := compose.NewService(svc).MuxLeafResolver(
			cmd.Context(), selection,
		); counterErr == nil && counter != nil {
			for _, t := range targets {
				if n, err := counter(cmd.Context(), t); err == nil {
					replicaCounts[t] = n
				}
			}
		}
	}

	return cli.RenderMuxWindows(cmd.OutOrStdout(), windows, replicaCounts, targets, format)
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

	selection, err := resolveComposeMuxSelection(cf)
	if err != nil {
		return err
	}
	spec := *selection.Spec.Mux

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	cfg := svc.Config()
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate cmdman binary: %w", err)
	}

	composeSvc := compose.NewService(svc)
	resolver, replicas, err := composeSvc.MuxLeafResolver(cmd.Context(), selection)
	if err != nil {
		return err
	}

	result, cycleErr := mux.CycleScale(cmd.Context(), mux.CycleScaleOptions{
		Spec:     spec,
		Resolver: resolver,
		Replicas: replicas,
		Opts: mux.PaneArgvOpts{
			Executable: exe,
			DataDir:    cfg.DataDir,
			RuntimeDir: cfg.RuntimeDir,
		},
		Identity:    selection.ProjectIdentity(),
		SessionName: session,
		Command:     command,
		Position:    position,
	})
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
		selection, err := resolveComposeMuxSelection(cf)
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
		selection, err := resolveComposeMuxSelection(cf)
		if err != nil || selection.Spec == nil || selection.Spec.Mux == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return collectCycleTargets(*selection.Spec.Mux), cobra.ShellCompDirectiveNoFileComp
	}
}

// resolveComposeMuxSelection resolves the compose project for the mux
// subcommand. An explicit -f/--file loads exactly that project; without it the
// project is auto-selected from the composes associated with the current
// directory that declare a "mux:" section (see compose.SelectMuxProject).
// Either way the resolved project must declare a "mux:" section.
func resolveComposeMuxSelection(cf *composeFlags) (compose.ProjectSelection, error) {
	opts := cf.normalizeOpts()
	if opts.File == "" {
		return compose.SelectMuxProject(opts)
	}
	selection, err := compose.LoadOrProject(opts)
	if err != nil {
		return compose.ProjectSelection{}, err
	}
	if selection.Spec == nil || selection.Spec.Mux == nil {
		return compose.ProjectSelection{}, errors.New(
			`compose mux: missing "mux:" section in compose file`,
		)
	}
	return selection, nil
}

// composeMuxWindowName derives the cmdman-owned window name for a compose
// project: "cmdman-<project>", or plain "cmdman" when the project is unnamed.
func composeMuxWindowName(selection compose.ProjectSelection) string {
	if selection.Project != "" {
		return "cmdman-" + selection.Project
	}
	return "cmdman"
}

// collectCycleTargets returns a sorted, deduplicated list of unpinned leaf
// command names (Scale == 0) from all layouts of spec. These are the commands
// that participate in cycle-scale.
func collectCycleTargets(spec mux.Spec) []string {
	seen := make(map[string]struct{})
	for _, layout := range spec.Layouts {
		collectUnpinnedLeafCommands(layout.Root, seen)
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// collectUnpinnedLeafCommands walks p recursively and adds the command name
// of each unpinned leaf (Scale == 0) to seen.
func collectUnpinnedLeafCommands(p mux.PaneSpec, seen map[string]struct{}) {
	if p.IsLeaf() {
		if p.Scale == 0 {
			seen[p.Command] = struct{}{}
		}
		return
	}
	for _, child := range p.Panes {
		collectUnpinnedLeafCommands(child, seen)
	}
}
