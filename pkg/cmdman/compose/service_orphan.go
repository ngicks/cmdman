package compose

import (
	"context"
	"fmt"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// handleOrphans processes the orphan list. When removeOrphan is false it emits
// a structured slog.Warn per orphan and returns no outcomes. When removeOrphan
// is true it removes stopped orphans and skips running ones (resolved-decision 4).
func (s *Service) handleOrphans(
	ctx context.Context,
	spec ComposeSpec,
	orphans []store.CommandEntry,
	removeOrphan bool,
) []ActionOutcome {
	if len(orphans) == 0 {
		return nil
	}

	outcomes := make([]ActionOutcome, 0, len(orphans))

	for _, orphan := range orphans {
		cmdName := ""
		if orphan.ConfigJSON != nil {
			cmdName = orphan.ConfigJSON.Labels[LabelCommand]
		}

		if !removeOrphan {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: orphan command detected",
				"project", spec.Project,
				"workdir", spec.WorkDir,
				"command", cmdName,
				"id", orphan.ID,
			)
			continue
		}

		// --remove-orphan path.
		// Running/starting orphans: skip and report.
		if orphan.State == model.StateRunning || orphan.State == model.StateStarting {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: orphan command is running; skipping removal",
				"project", spec.Project,
				"workdir", spec.WorkDir,
				"command", cmdName,
				"id", orphan.ID,
				"state", orphan.State,
			)
			outcomes = append(outcomes, ActionOutcome{
				Command: cmdName,
				Action:  "skipped",
				Err: fmt.Errorf(
					"orphan command %q is %s; removal skipped (stop first)",
					cmdName,
					orphan.State,
				),
			})
			continue
		}

		// Stopped orphan: remove it.
		results, err := s.svc.Remove(ctx, cmdman.RemoveRequest{
			Targets: []string{orphan.ID},
		})
		if err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: failed to remove orphan command",
				"project", spec.Project,
				"workdir", spec.WorkDir,
				"command", cmdName,
				"id", orphan.ID,
				"error", err,
			)
			outcomes = append(outcomes, ActionOutcome{
				Command: cmdName,
				Action:  "remove-orphan",
				Err:     fmt.Errorf("remove orphan command %q (%s): %w", cmdName, orphan.ID, err),
			})
			continue
		}
		var removeErr error
		for _, r := range results {
			if r.Err != nil {
				removeErr = r.Err
				break
			}
		}
		if removeErr != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose: failed to remove orphan command",
				"project", spec.Project,
				"workdir", spec.WorkDir,
				"command", cmdName,
				"id", orphan.ID,
				"error", removeErr,
			)
			outcomes = append(outcomes, ActionOutcome{
				Command: cmdName,
				Action:  "remove-orphan",
				Err: fmt.Errorf(
					"remove orphan command %q (%s): %w",
					cmdName,
					orphan.ID,
					removeErr,
				),
			})
			continue
		}

		contextkey.ValueSlogLoggerDefault(ctx).Info("compose: removed orphan command",
			"project", spec.Project,
			"workdir", spec.WorkDir,
			"command", cmdName,
			"id", orphan.ID,
		)
		outcomes = append(outcomes, ActionOutcome{
			Command: cmdName,
			Action:  "remove-orphan",
		})
	}

	return outcomes
}
