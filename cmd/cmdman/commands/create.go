package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// createFlags holds all flags shared between `create` and `run`.
type createFlags struct {
	Name            string
	Dir             string
	Env             []string
	Label           []string
	Restart         string
	StopSignal      string
	Rm              bool
	ScrollbackBytes int
	LogDriver       string
	LogOpts         []string
}

func bindCreateFlags(cmd *cobra.Command, f *createFlags) {
	flags := cmd.Flags()
	flags.StringVarP(&f.Name, "name", "n", "", "Human-readable unique name")
	flags.StringVarP(&f.Dir, "dir", "C", "", "Working directory for the command")
	flags.StringArrayVarP(&f.Env, "env", "E", nil, "Environment variable KEY=VALUE (repeatable)")
	flags.StringArrayVarP(&f.Label, "label", "l", nil, "Metadata label KEY=VALUE (repeatable)")
	flags.StringVar(
		&f.Restart,
		"restart",
		string(store.RestartPolicyNo),
		"Restart policy: no, on-failure, always",
	)
	flags.StringVar(&f.StopSignal, "stop-signal", store.DefaultStopSignal, "Default stop signal")
	flags.BoolVar(&f.Rm, "rm", false, "Auto-remove on exit")
	flags.IntVar(
		&f.ScrollbackBytes,
		"scrollback-bytes",
		store.DefaultScrollbackBytes,
		"Scrollback buffer size in bytes",
	)
	flags.StringVar(
		&f.LogDriver,
		"log-driver",
		"",
		"Log driver: k8s-file, none (default from config)",
	)
	flags.StringArrayVar(
		&f.LogOpts,
		"log-opt",
		nil,
		"Log driver option KEY=VALUE (repeatable)",
	)
}

func createCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flags createFlags

	cmd := &cobra.Command{
		Use:   "create [flags] -- COMMAND [ARGS...]",
		Short: "Create a new command without starting it",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, rootCfg, &flags)
		},
	}

	bindCreateFlags(cmd, &flags)

	parent.AddCommand(cmd)
}

func runCreate(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	flags *createFlags,
) error {
	id, name, err := doCreate(cmd, args, rootCfg, flags)
	if err != nil {
		return err
	}
	displayName := id
	if name != "" {
		displayName = name
	}
	fmt.Fprintln(cmd.OutOrStdout(), displayName)
	return nil
}

// doCreate creates a command entry in the store and returns the generated ID and name.
func doCreate(
	cmd *cobra.Command,
	args []string,
	rootCfg *cmdman.CmdmanConfig,
	flags *createFlags,
) (id, name string, err error) {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return "", "", err
	}

	labels, err := parseLabels(flags.Label)
	if err != nil {
		return "", "", err
	}

	logOpts, err := parseLogOpts(flags.LogOpts)
	if err != nil {
		return "", "", err
	}

	result, err := svc.Create(cmd.Context(), cmdman.CreateRequest{
		Name:            flags.Name,
		Dir:             flags.Dir,
		Env:             flags.Env,
		Labels:          labels,
		RestartPolicy:   store.RestartPolicy(flags.Restart),
		StopSignal:      flags.StopSignal,
		AutoRemove:      flags.Rm,
		ScrollbackBytes: flags.ScrollbackBytes,
		LogDriver:       store.LogDriver(flags.LogDriver),
		LogOpts:         logOpts,
		Argv:            args,
	})
	if err != nil {
		return "", "", err
	}
	return result.ID, result.Name, nil
}
