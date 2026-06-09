package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func composeScaleCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig, cf *composeFlags) {
	var flagProgress string

	cmd := &cobra.Command{
		Use:   "scale [flags] SERVICE=NUM [SERVICE=NUM...]",
		Short: "Set the number of replicas for compose commands",
		Long: `Set the desired replica count of one or more compose commands and
reconcile to it: missing replicas are created and started, surplus replicas
(from a scale-down) are stopped and removed.

Each replica is a distinct cmdman command named "<command>-<index>" for index
1..NUM. The scale is an ephemeral override of the compose file's scale:; a later
"compose up" reverts to the file unless the file is edited.`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeComposeCommands(rootCfg, cf),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runComposeScale(cmd, rootCfg, cf, args, flagProgress)
		},
	}

	cmd.Flags().StringVar(&flagProgress, "progress", "auto", cli.ProgressFlagUsage)
	_ = cmd.RegisterFlagCompletionFunc("progress", progressCompletions)

	parent.AddCommand(cmd)
}

func runComposeScale(
	cmd *cobra.Command,
	rootCfg *cmdman.CmdmanConfig,
	cf *composeFlags,
	args []string,
	progress string,
) error {
	scales, err := parseScaleArgs(args)
	if err != nil {
		return err
	}

	spec, err := compose.LoadAndNormalize(cf.normalizeOpts())
	if err != nil {
		return err
	}
	if err := applyScaleOverrides(&spec, scales); err != nil {
		return err
	}

	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	defer svc.Close()

	prog, err := resolveComposeProgress(cmd, progress, "up")
	if err != nil {
		return err
	}
	defer prog.Close()

	names := make([]string, 0, len(scales))
	for name := range scales {
		names = append(names, name)
	}

	result, err := compose.NewService(svc, compose.WithReporter(prog)).Up(
		cmd.Context(), spec, compose.UpOption{
			CreateOption: compose.CreateOption{CommandNames: names},
			StartOption:  compose.StartOption{CommandNames: names},
		})
	if err != nil {
		return err
	}
	return cli.UpResultErr(result)
}

// parseScaleArgs parses "SERVICE=NUM" arguments into a service→count map,
// rejecting malformed entries, non-positive counts, and duplicate services.
func parseScaleArgs(args []string) (map[string]int, error) {
	scales := make(map[string]int, len(args))
	for _, arg := range args {
		name, numStr, ok := strings.Cut(arg, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid scale argument %q: want SERVICE=NUM", arg)
		}
		num, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("invalid scale for %q: %q is not a number", name, numStr)
		}
		if num < 1 {
			return nil, fmt.Errorf("invalid scale for %q: must be >= 1, got %d", name, num)
		}
		if _, dup := scales[name]; dup {
			return nil, fmt.Errorf("service %q specified more than once", name)
		}
		scales[name] = num
	}
	return scales, nil
}

// applyScaleOverrides sets the requested replica counts on the matching spec
// commands, erroring when a named service is not declared in the compose file.
func applyScaleOverrides(spec *compose.ComposeSpec, scales map[string]int) error {
	index := make(map[string]int, len(spec.Commands))
	for i, c := range spec.Commands {
		index[c.Name] = i
	}
	for name, num := range scales {
		i, ok := index[name]
		if !ok {
			return fmt.Errorf("unknown compose command %q", name)
		}
		spec.Commands[i].Scale = num
	}
	return nil
}
