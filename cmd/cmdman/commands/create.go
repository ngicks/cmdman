package commands

import (
	"fmt"

	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/ngicks/crabswarm/pkg/cmdman/store"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(createCmd)
	addCreateFlags(createCmd)
}

func addCreateFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringP("name", "n", "", "Human-readable unique name")
	f.StringP("dir", "C", "", "Working directory for the command")
	f.StringArrayP("env", "E", nil, "Environment variable KEY=VALUE (repeatable)")
	f.StringArrayP("label", "l", nil, "Metadata label KEY=VALUE (repeatable)")
	f.String("restart", string(store.RestartPolicyNo), "Restart policy: no, on-failure, always")
	f.String("stop-signal", store.DefaultStopSignal, "Default stop signal")
	f.Bool("rm", false, "Auto-remove on exit")
	f.Int("scrollback-bytes", store.DefaultScrollbackBytes, "Scrollback buffer size in bytes")
}

var createCmd = &cobra.Command{
	Use:   "create [flags] -- COMMAND [ARGS...]",
	Short: "Create a new command without starting it",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runCreate,
}

func runCreate(cmd *cobra.Command, args []string) error {
	id, name, err := doCreate(cmd, args)
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
func doCreate(cmd *cobra.Command, args []string) (id, name string, err error) {
	svc, err := cmdmanService()
	if err != nil {
		return "", "", err
	}

	f := cmd.Flags()
	name, _ = f.GetString("name")
	dir, _ := f.GetString("dir")
	envSlice, _ := f.GetStringArray("env")
	labelSlice, _ := f.GetStringArray("label")
	restartPolicy, _ := f.GetString("restart")
	stopSignal, _ := f.GetString("stop-signal")
	autoRemove, _ := f.GetBool("rm")
	scrollbackBytes, _ := f.GetInt("scrollback-bytes")

	labels, err := parseLabels(labelSlice)
	if err != nil {
		return "", "", err
	}

	result, err := svc.Create(cmd.Context(), cmdman.CreateRequest{
		Name:            name,
		Dir:             dir,
		Env:             envSlice,
		Labels:          labels,
		RestartPolicy:   store.RestartPolicy(restartPolicy),
		StopSignal:      stopSignal,
		AutoRemove:      autoRemove,
		ScrollbackBytes: scrollbackBytes,
		Argv:            args,
	})
	if err != nil {
		return "", "", err
	}
	return result.ID, result.Name, nil
}
