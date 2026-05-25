package compose

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/go-common/contextkey"
)

// SendKeysOption configures a compose SendKeys operation.
type SendKeysOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
	// Keys is the key sequence to send to each targeted command's PTY (required).
	Keys []string
	// Literal sends keys verbatim without translating key names.
	Literal bool
	// Hex treats keys as hexadecimal byte values.
	Hex bool
	// RepeatCount repeats the key sequence N times (values <= 0 mean once).
	RepeatCount int
}

// SendKeysResult is the aggregated result of a compose send-keys operation.
type SendKeysResult struct {
	Outcomes []SendKeysOutcome
}

// SendKeysOutcome records the result of sending keys to a single compose command.
type SendKeysOutcome struct {
	Command string
	Err     error
}

// SendKeys sends a key sequence to the PTYs of project-labeled commands.
//
// Like Signal, this broadcasts to every targeted command; CommandNames narrows
// the set. Keys is required; an empty value returns an error before any RPC.
// Per resolved-decision 15, an empty project target set returns no outcomes and
// logs a warning. Per resolved-decision 21, failures are aggregated and every
// command in the set is attempted.
func (s *Service) SendKeys(
	ctx context.Context,
	selection ProjectSelection,
	opts SendKeysOption,
) (*SendKeysResult, error) {
	if len(opts.Keys) == 0 {
		return nil, fmt.Errorf("compose send-keys: at least one key is required")
	}

	entries, err := s.svc.List(ctx, cmdman.ListRequest{
		AllStates: true,
		Labels:    projectLabels(selection.WorkDir, selection.Project),
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
		contextkey.ValueSlogLoggerDefault(ctx).Warn("compose send-keys: no commands found for project",
			"project", selection.Project,
			"workdir", selection.WorkDir,
			"operation", "send-keys",
		)
		return &SendKeysResult{}, nil
	}

	req := cmdman.SendKeysRequest{
		Keys:        opts.Keys,
		Literal:     opts.Literal,
		Hex:         opts.Hex,
		RepeatCount: opts.RepeatCount,
	}

	var (
		mu       sync.Mutex
		outcomes []SendKeysOutcome
	)
	eg, _ := errgroup.WithContext(ctx)

	for _, entry := range entries {
		id := entry.ID
		name := commandNameOf(entry)

		eg.Go(func() error {
			err := s.svc.SendKeys(ctx, id, req)
			outcome := SendKeysOutcome{Command: name}
			if err != nil {
				outcome.Err = fmt.Errorf("send-keys command %q (%s): %w", name, id, err)
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose send-keys: send failed",
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
	return &SendKeysResult{Outcomes: outcomes}, nil
}
