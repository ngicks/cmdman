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
