package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

func composeMuxCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var (
		flagSession string
		flagDetach  bool
	)

	cmd := &cobra.Command{
		Use:   "mux [layout]",
		Short: "Open a multiplexer dashboard for a compose project",
		Long: `Open a multiplexer dashboard described by the compose file's "mux:"
section. Each leaf references a compose service name; panes run
cmdman attach by default, or cmdman logs when mode: logs.

With no argument, mux cycles to the next layout each invocation (a fresh window
starts at the first layout). Pass a layout name or a 0-based index to apply that
layout directly; the choice becomes the new cycle position. A name is matched
before an index, so a layout literally named "2" wins over index 2.

With no --session, the dashboard targets the current tmux session when run
inside tmux, otherwise a session named "cmdman".

With --detach, the dashboard window is torn down instead of opened (the layout
argument is ignored): the in-pane viewers are detached, the window collapses to
a single clean pane, and the tmux options cmdman set are cleared. The supervised
commands keep running.

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeMux(cmd, rootCfg, cf, args, flagSession, flagDetach)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
	)
	cmd.Flags().BoolVar(
		&flagDetach, "detach", false,
		"Tear down the dashboard: restore the window to one clean pane and clear cmdman's tmux options",
	)
	parent.AddCommand(cmd)
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
		selection, err := compose.LoadOrProject(cf.normalizeOpts())
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

// uniqueMuxSelection auto-selects the sole default-dir compose project that has
// a mux: section, for `compose mux` invocations with no explicit project (-f)
// and no CWD compose file. It errors when none or more than one project has a
// mux: section (the ambiguous case).
func uniqueMuxSelection() (compose.ProjectSelection, error) {
	projects, err := compose.ListMuxProjects()
	if err != nil {
		return compose.ProjectSelection{}, err
	}
	switch len(projects) {
	case 0:
		return compose.ProjectSelection{}, errors.New(
			`compose mux: no compose file found, and no project with a "mux:" section ` +
				"in the default compose dir; pass -f <file|project>")
	case 1:
		p := projects[0]
		return compose.ProjectSelection{
			Spec:    &p.Spec,
			WorkDir: p.Spec.WorkDir,
			Project: p.Spec.Project,
		}, nil
	default:
		names := make([]string, len(projects))
		for i, p := range projects {
			names[i] = p.Name
		}
		return compose.ProjectSelection{}, fmt.Errorf(
			`compose mux: multiple projects have a "mux:" section (%s); select one with -f`,
			strings.Join(names, ", "))
	}
}

func runComposeMux(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	args []string,
	session string,
	detach bool,
) error {
	selection, err := resolveComposeMuxSelection(cf)
	if err != nil {
		return err
	}
	spec := *selection.Spec.Mux
	windowName := composeMuxWindowName(selection)

	if detach {
		// Detach needs no cmdman service or leaf resolution — only the spec's
		// driver / driver_opt and the project-derived window name. The layout
		// argument is irrelevant to teardown and is ignored.
		return mux.Detach(cmd.Context(), mux.DetachOptions{
			Driver:      spec.Driver,
			DriverOpt:   spec.DriverOpt,
			SessionName: session,
			WindowName:  windowName,
			Stdout:      cmd.OutOrStdout(),
		})
	}

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
	resolver := func(ctx context.Context, leafName string) (string, error) {
		return composeSvc.ResolveCommandID(ctx, selection, leafName)
	}

	built, err := mux.Build(cmd.Context(), spec, resolver, mux.PaneArgvOpts{
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
		Layout:      layout,
		Stdout:      cmd.OutOrStdout(),
	})
}

// resolveComposeMuxSelection loads the compose project for the mux subcommand,
// falling back to the sole default-dir project that has a "mux:" section when
// no explicit project (-f) or CWD compose file is given. It errors when the
// resolved project has no "mux:" section.
func resolveComposeMuxSelection(cf *composeFlags) (compose.ProjectSelection, error) {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return compose.ProjectSelection{}, err
	}
	if selection.Spec == nil {
		selection, err = uniqueMuxSelection()
		if err != nil {
			return compose.ProjectSelection{}, err
		}
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
