package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/config"
	"github.com/ngicks/cmdman/internal/cmdsignals"
)

// loadConfig assembles the configuration for cmd: config.Load layers defaults <
// config file < environment, then the explicitly-set root flags win on top.
// cmd.Flags() sees the root's persistent flags, so Changed reports what the user
// actually passed - an unset flag never overwrites a lower layer with its
// zero-value default.
func loadConfig(cmd *cobra.Command, rf *rootFlags) (cmdman.CmdmanConfig, error) {
	cfg, err := config.Load(rf.config)
	if err != nil {
		return cmdman.CmdmanConfig{}, err
	}
	if cmd.Flags().Changed("data-dir") {
		cfg.DataDir = rf.dataDir
	}
	if cmd.Flags().Changed("runtime-dir") {
		cfg.RuntimeDir = rf.runtimeDir
	}
	if err := cfg.Validate(); err != nil {
		return cmdman.CmdmanConfig{}, err
	}
	return cfg, nil
}

func cmdmanService(cmd *cobra.Command, rf *rootFlags) (*cmdman.Service, error) {
	cfg, err := loadConfig(cmd, rf)
	if err != nil {
		return nil, err
	}
	return cmdman.NewService(cfg), nil
}

func parseLabels(labelSlice []string) (map[string]string, error) {
	if len(labelSlice) == 0 {
		return nil, nil
	}
	labels := make(map[string]string)
	for _, l := range labelSlice {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label format: %s (expected KEY=VALUE)", l)
		}
		labels[k] = v
	}
	return labels, nil
}

func parseLogOpts(opts []string) (map[string]string, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(opts))
	for _, o := range opts {
		k, v, ok := strings.Cut(o, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid log-opt format: %s (expected KEY=VALUE)", o)
		}
		out[k] = v
	}
	return out, nil
}

// attachSignalHooks returns the [cli.AttachOptions] PauseSignals /
// ResumeSignals hooks bound to the root Notifier stored in ctx.
//
// While attached, the root canceling handler is swapped for one that discards
// the signals: cli.Attach's own os/signal registration — installed by install
// and removed by remove — forwards them to the remote command instead. Both
// hooks report false when ctx carries no Notifier, which makes cli.Attach fall
// back to forwarding without touching the process-global handler.
func attachSignalHooks(ctx context.Context) (
	pause func(install func()) bool,
	resume func(remove func()) bool,
) {
	var discard *cmdsignals.Handler
	pause = func(install func()) bool {
		n, ok := cmdsignals.CtxValueNotifier(ctx)
		if !ok {
			return false
		}
		discard = cmdsignals.NewHandler(1, nil, nil)
		go discard.Run()
		// Swap before install: a signal landing in the gap is dropped rather
		// than cancelling the CLI.
		n.Swap(discard)
		install()
		return true
	}
	resume = func(remove func()) bool {
		n, ok := cmdsignals.CtxValueNotifier(ctx)
		if !ok {
			return false
		}
		n.Restore()
		remove()
		discard.Stop()
		return true
	}
	return pause, resume
}
