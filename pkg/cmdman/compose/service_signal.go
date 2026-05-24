package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// SignalOption configures a Signal operation.
type SignalOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
	// Signal is the signal name or number to send (required).
	// Accepted forms: "SIGTERM", "TERM", "15" (numeric).
	Signal string
}

// SignalResult is the aggregated result of a compose signal operation.
type SignalResult struct {
	Outcomes []SignalOutcome
}

// SignalOutcome records the result of signaling a single compose command.
type SignalOutcome struct {
	Command string
	Err     error
}

// Signal sends a signal to project-labeled commands.
//
// Signal is required; an empty value returns an error before any network call.
// The signal value is parsed once via store.ParseSignal; invalid values are
// rejected up front.
//
// Per resolved-decision 15, an empty project target set exits 0 with a
// structured log event. Per resolved-decision 21, failures are aggregated and
// every command in the set is attempted.
func (s *Service) Signal(
	ctx context.Context,
	selection ProjectSelection,
	opts SignalOption,
) (*SignalResult, error) {
	if opts.Signal == "" {
		return nil, fmt.Errorf("compose signal: --signal is required")
	}

	sig, _, err := store.ParseSignal(opts.Signal)
	if err != nil {
		return nil, fmt.Errorf("compose signal: invalid signal %q: %w", opts.Signal, err)
	}

	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels: map[string]string{
			LabelWorkdir: selection.WorkDir,
			LabelProject: selection.Project,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list project commands: %w", err)
	}

	if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
		return nil, err
	}
	if len(opts.CommandNames) > 0 {
		entries = filterByCommandNames(entries, opts.CommandNames)
	}

	if len(entries) == 0 {
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose signal: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "signal",
		)
		return &SignalResult{}, nil
	}

	var (
		mu       sync.Mutex
		outcomes []SignalOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}
		id := entry.ID
		name := cmdName

		eg.Go(func() error {
			err := s.svc.Signal(ctx, id, sig)
			outcome := SignalOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("signal command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose signal: signal failed",
					"project", selection.Project,
					"command", name,
					"id", id,
					"error", err,
				)
			}
			mu.Lock()
			outcomes = append(outcomes, outcome)
			mu.Unlock()
			return nil // always nil — aggregate, never short-circuit
		})
	}

	_ = eg.Wait()
	return &SignalResult{Outcomes: outcomes}, nil
}
