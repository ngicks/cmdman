package commands

import (
	"errors"
	"fmt"
	"os"

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

Subcommands: up, down, ls. A layout literally named "up", "down", or "ls" must
be passed as: cmdman compose mux up <name>.`,
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
	composeMuxLsCmd(cmd, cf, &flagSession)

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

func composeMuxLsCmd(parent *cobra.Command, cf *composeFlags, parentSession *string) {
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

Columns: SESSION, WINDOW, ID, IDENTITY, LAYOUT (-1 displayed as "-").`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sess := flagSession
			if !cmd.Flags().Changed("session") && parentSession != nil {
				sess = *parentSession
			}
			return runComposeMuxLs(cmd, cf, sess, flagFormat)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Narrow listing to this tmux session only (default: server-wide)",
	)
	cmd.Flags().StringVar(&flagFormat, "format", "", cli.MuxLsFormatUsage())

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

	built, err := mux.Build(cmd.Context(), spec, resolver, replicas, mux.PaneArgvOpts{
		Executable: exe,
		DataDir:    cfg.DataDir,
		RuntimeDir: cfg.RuntimeDir,
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

func runComposeMuxLs(cmd *cobra.Command, cf *composeFlags, session, format string) error {
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
	return cli.RenderMuxWindows(cmd.OutOrStdout(), windows, format)
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
