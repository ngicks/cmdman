package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/cli"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func cmdmanService(rootCfg *cmdman.CmdmanConfig) (*cmdman.Service, error) {
	cfg, err := rootCfg.WithDefaults()
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

// stickyStateFor returns a cli.StickyHooks-shaped State closure that asks
// the cmdman service for the current state of idOrName and renders it for
// the wait prompt.
func stickyStateFor(
	svc *cmdman.Service,
	idOrName string,
) func(ctx context.Context) (cli.StickyState, error) {
	return func(ctx context.Context) (cli.StickyState, error) {
		out, err := svc.Inspect(ctx, idOrName)
		if err != nil {
			return cli.StickyState{Status: fmt.Sprintf("inspect failed: %v", err)}, nil
		}
		return renderStickyState(out.State, out.ExitCode), nil
	}
}

// renderStickyState turns a state + optional exit code into a [cli.StickyState].
// Running is true only when the command is observably alive.
func renderStickyState(state model.EventType, exitCode *int) cli.StickyState {
	switch state {
	case model.EventTypeStarted, model.EventTypeStarting:
		return cli.StickyState{Running: true, Status: string(state)}
	case model.EventTypeExited, model.EventTypeFailed:
		if exitCode != nil {
			return cli.StickyState{Status: fmt.Sprintf("%s (code %d)", state, *exitCode)}
		}
		return cli.StickyState{Status: string(state)}
	default:
		// Created, Stopped, "" / unknown
		s := string(state)
		if s == "" {
			s = "not running"
		}
		return cli.StickyState{Status: s}
	}
}
