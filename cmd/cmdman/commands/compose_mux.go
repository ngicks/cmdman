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
	var flagSession string

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

The compose file must contain a top-level "mux:" section; a missing section
is an error (no synthesized default).`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeComposeMuxLayout(cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeMux(cmd, rootCfg, cf, args, flagSession)
		},
	}
	cmd.Flags().StringVarP(
		&flagSession, "session", "s", "",
		"Target tmux session (default: current session when inside tmux, else cmdman)",
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
) error {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return err
	}
	if selection.Spec == nil {
		// No explicit project (-f) or CWD compose file: fall back to the sole
		// default-dir project that has a mux: section, when unambiguous.
		selection, err = uniqueMuxSelection()
		if err != nil {
			return err
		}
	}
	if selection.Spec == nil || selection.Spec.Mux == nil {
		return errors.New(`compose mux: missing "mux:" section in compose file`)
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

	windowName := "cmdman"
	if selection.Project != "" {
		windowName = "cmdman-" + selection.Project
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
