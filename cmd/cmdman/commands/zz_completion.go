package commands

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// runningStates are the states in which a command owns a live monitor / PTY and
// can be attached to, signalled, or stopped.
var runningStates = []model.EventType{model.EventTypeStarting, model.EventTypeRunning}

// completeCommandNames returns a [cobra.CompletionFunc] that completes a command
// target (NAME, falling back to ID) from the cmdman store. When states is
// non-empty only commands in one of those states are offered. Already-supplied
// positional arguments are filtered out so repeated targets are not
// re-suggested.
func completeCommandNames(
	rootCfg *cmdman.CmdmanConfig,
	states ...model.EventType,
) cobra.CompletionFunc {
	stateSet := make(map[model.EventType]struct{}, len(states))
	for _, s := range states {
		stateSet[s] = struct{}{}
	}

	return func(
		cmd *cobra.Command,
		args []string,
		toComplete string,
	) ([]cobra.Completion, cobra.ShellCompDirective) {
		svc, err := cmdmanService(rootCfg)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer svc.Close()

		entries, err := svc.List(cmd.Context(), cmdman.ListRequest{AllStates: true})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		seen := make(map[string]struct{}, len(args))
		for _, a := range args {
			seen[a] = struct{}{}
		}

		var out []cobra.Completion
		for _, e := range entries {
			if len(stateSet) > 0 {
				if _, ok := stateSet[e.State]; !ok {
					continue
				}
			}
			cand := e.Name
			if cand == "" {
				cand = e.ID
			}
			if _, ok := seen[cand]; ok {
				continue
			}
			if !strings.HasPrefix(cand, toComplete) {
				continue
			}
			out = append(out, cobra.CompletionWithDesc(cand, string(e.State)))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeComposeCommands returns a [cobra.CompletionFunc] that completes the
// service names of the selected compose project. Names come from the loaded
// compose spec when available, otherwise from the project's stored commands.
// Already-supplied positional arguments are filtered out.
func completeComposeCommands(
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
) cobra.CompletionFunc {
	return func(
		cmd *cobra.Command,
		args []string,
		toComplete string,
	) ([]cobra.Completion, cobra.ShellCompDirective) {
		// Keys after a `--` separator (e.g. compose send-keys) are arbitrary and
		// must not be completed as command names.
		if dash := cmd.ArgsLenAtDash(); dash >= 0 && len(args) >= dash {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		names := composeCommandNames(cmd, rootCfg, cf)

		seen := make(map[string]struct{}, len(args))
		for _, a := range args {
			seen[a] = struct{}{}
		}

		var out []cobra.Completion
		for _, n := range names {
			if _, ok := seen[n]; ok {
				continue
			}
			if !strings.HasPrefix(n, toComplete) {
				continue
			}
			out = append(out, n)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// composeCommandNames resolves the candidate compose service names for the
// current selection, returning nil on any error so completion degrades to no
// suggestions rather than failing.
func composeCommandNames(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
) []string {
	selection, err := compose.LoadOrProject(cf.normalizeOpts())
	if err != nil {
		return nil
	}

	if selection.Spec != nil {
		names := make([]string, 0, len(selection.Spec.Commands))
		for _, c := range selection.Spec.Commands {
			names = append(names, c.Name)
		}
		return names
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return nil
	}
	defer svc.Close()

	statuses, err := compose.NewService(svc).Ps(cmd.Context(), selection, nil)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(statuses))
	for _, s := range statuses {
		names = append(names, s.Command)
	}
	return names
}

// completeComposeFile completes the compose -f/--file flag with the names of
// projects discoverable under the default compose dir, while still letting the
// shell offer its default file-path completion (ShellCompDirectiveDefault).
func completeComposeFile(
	_ *cobra.Command,
	_ []string,
	toComplete string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	names, err := compose.ListNamedProjects()
	if err != nil {
		return nil, cobra.ShellCompDirectiveDefault
	}
	var out []cobra.Completion
	for _, n := range names {
		if strings.HasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out, cobra.ShellCompDirectiveDefault
}

// signalCompletions offers the common POSIX signal names. hrstr.ParseSignal also
// accepts bare names (TERM) and numbers (15); these canonical hints cover the
// usual cases without enumerating every platform signal.
var signalCompletions = cobra.FixedCompletions(
	[]cobra.Completion{
		"SIGTERM", "SIGKILL", "SIGINT", "SIGHUP", "SIGQUIT",
		"SIGUSR1", "SIGUSR2", "SIGSTOP", "SIGCONT", "SIGWINCH",
	},
	cobra.ShellCompDirectiveNoFileComp,
)

// waitConditionCompletions offers the states accepted by `wait --condition`.
var waitConditionCompletions = cobra.FixedCompletions(
	[]cobra.Completion{
		string(model.EventTypeStopped),
		string(model.EventTypeCreated),
		string(model.EventTypeStarting),
		string(model.EventTypeRunning),
		string(model.EventTypeExited),
		string(model.EventTypeFailed),
	},
	cobra.ShellCompDirectiveNoFileComp,
)

// progressCompletions offers the modes accepted by the compose `--progress` flag.
var progressCompletions = cobra.FixedCompletions(
	[]cobra.Completion{"auto", "tty", "json", "quiet"},
	cobra.ShellCompDirectiveNoFileComp,
)

// restartPolicyCompletions offers the policies accepted by `--restart`.
var restartPolicyCompletions = cobra.FixedCompletions(
	[]cobra.Completion{"no", "on-failure", "always"},
	cobra.ShellCompDirectiveNoFileComp,
)

// logDriverCompletions offers the drivers accepted by `--log-driver`.
var logDriverCompletions = cobra.FixedCompletions(
	[]cobra.Completion{"k8s-file", "none"},
	cobra.ShellCompDirectiveNoFileComp,
)
