package compose

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ngicks/go-common/contextkey"
	"github.com/ngicks/go-iterator-helper/hiter"
	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// logsChannelBuffer bounds in-flight log messages so a momentarily slow
// consumer applies backpressure instead of unbounded buffering.
const logsChannelBuffer = 64

// LogsOption configures a Logs operation.
type LogsOption struct {
	// CommandNames optionally narrows the target set to specific compose command names.
	CommandNames []string
	// Follow tails live output when true; otherwise reads stored logs and exits.
	Follow bool
	// Since excludes log records before this time (zero = no lower bound).
	Since time.Time
	// Until excludes log records after this time (zero = no upper bound).
	Until time.Time
	// Head returns only the first N records per command (0 = no limit).
	Head int
	// Tail returns only the last N records per command (0 = no limit).
	Tail int
}

// LogMessage is a single log record emitted by a project-labeled command,
// tagged with the compose command name so the presentation layer can prefix
// and route it. Only successful lines flow on the message channel; open, read,
// and close errors are aggregated into the Logs error channel instead.
type LogMessage struct {
	// Command is the compose command name (YAML map key).
	Command string
	// Record is the underlying log record (line, stream, timestamp).
	Record logdriver.Record
}

// Logs streams log output for project-labeled commands over a channel, leaving
// presentation (prefixing, stdout/stderr routing) to the caller.
//
// It returns two channels: the first delivers log messages; the second delivers
// a single terminal error (nil on success) once the stream ends. Cancel ctx to
// stop following — both channels are then closed.
//
// When Follow=false, each command's records are read, merged in timestamp
// order, then sent. When Follow=true, each command's reader runs in its own
// goroutine and messages are sent as they arrive; cross-command ordering is not
// stabilized (resolved-decision 2). Per resolved-decision 15, an empty project
// target set ends with a nil error. Per resolved-decision 21, follow-mode
// errors are aggregated and all commands are attempted.
func (s *Service) Logs(
	ctx context.Context,
	selection ProjectSelection,
	opts LogsOption,
) (<-chan LogMessage, <-chan error) {
	out := make(chan LogMessage, logsChannelBuffer)
	errc := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errc)

		entries, err := s.svc.List(ctx, cmdman.ListRequest{
			AllStates: true,
			Labels: map[string]string{
				LabelWorkdir: selection.WorkDir,
				LabelProject: selection.Project,
			},
		})
		if err != nil {
			errc <- fmt.Errorf("list project commands: %w", err)
			return
		}

		if err := validateCommandNames(opts.CommandNames, selection.Spec, entries); err != nil {
			errc <- err
			return
		}
		if len(opts.CommandNames) > 0 {
			entries = filterByCommandNames(entries, opts.CommandNames)
		}

		if len(entries) == 0 {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose logs: no commands found for project",
				"project", selection.Project,
				"workdir", selection.WorkDir,
				"operation", "logs",
			)
			return
		}

		if opts.Follow {
			errc <- s.logsFollow(ctx, selection.Project, entries, opts, out)
			return
		}
		errc <- s.logsMerged(ctx, selection.Project, entries, opts, out)
	}()

	return out, errc
}

// logsMerged reads all per-command records, merges by timestamp, and sends
// prefixable messages on out. Open/read/close errors are aggregated and
// returned.
func (s *Service) logsMerged(
	ctx context.Context,
	project string,
	entries []cmdmanEntry,
	opts LogsOption,
	out chan<- LogMessage,
) error {
	type namedRecord struct {
		cmdName string
		rec     logdriver.Record
	}

	// Collect per-command record slices.
	allSeqs := make([][]namedRecord, 0, len(entries))
	var errs []error

	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}

		reader, err := s.svc.Logs(ctx, cmdman.LogsRequest{
			IDOrName: entry.ID,
			Follow:   false,
			Since:    opts.Since,
			Until:    opts.Until,
			Head:     opts.Head,
			Tail:     opts.Tail,
		})
		if err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose logs: open reader failed",
				"project", project,
				"command", cmdName,
				"id", entry.ID,
				"error", err,
			)
			errs = append(
				errs,
				fmt.Errorf("open logs for command %q (%s): %w", cmdName, entry.ID, err),
			)
			continue
		}

		var recs []namedRecord
		for rec := range reader.Records() {
			recs = append(recs, namedRecord{cmdName: cmdName, rec: rec})
		}
		if err := reader.Close(); err != nil {
			errs = append(
				errs,
				fmt.Errorf("close logs for command %q (%s): %w", cmdName, entry.ID, err),
			)
		}
		allSeqs = append(allSeqs, recs)
	}

	if len(allSeqs) == 0 {
		return errors.Join(errs...)
	}

	// Build a merged iterator over all record slices, ordered by timestamp.
	cmp := func(a, b namedRecord) int {
		ta := a.rec.Line.Time
		tb := b.rec.Line.Time
		if ta.Before(tb) {
			return -1
		}
		if ta.After(tb) {
			return 1
		}
		return 0
	}

	// Fold multiple slices into a single merged sequence using hiter.MergeFunc.
	merged := hiter.MergeSortFunc(allSeqs[0], cmp)
	for _, seq := range allSeqs[1:] {
		merged = hiter.MergeFunc(cmp, merged, hiter.MergeSortFunc(seq, cmp))
	}

	for nr := range merged {
		if nr.rec.Err != nil {
			contextkey.ValueSlogLoggerDefault(ctx).Warn("compose logs: record error",
				"project", project,
				"command", nr.cmdName,
				"error", nr.rec.Err,
			)
			errs = append(errs, fmt.Errorf("read logs for command %q: %w", nr.cmdName, nr.rec.Err))
			continue
		}
		select {
		case out <- LogMessage{Command: nr.cmdName, Record: nr.rec}:
		case <-ctx.Done():
			return errors.Join(errs...)
		}
	}
	return errors.Join(errs...)
}

// logsFollow opens per-command readers concurrently and sends prefixable
// messages on out as they arrive. All goroutines are attempted; errors are
// aggregated.
func (s *Service) logsFollow(
	ctx context.Context,
	project string,
	entries []cmdmanEntry,
	opts LogsOption,
	out chan<- LogMessage,
) error {
	var (
		mu   sync.Mutex
		errs []error
	)
	addErr := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	eg, egCtx := errgroup.WithContext(ctx)

	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}
		id := entry.ID
		name := cmdName

		eg.Go(func() error {
			reader, err := s.svc.Logs(egCtx, cmdman.LogsRequest{
				IDOrName: id,
				Follow:   true,
				Since:    opts.Since,
				Until:    opts.Until,
				Head:     opts.Head,
				Tail:     opts.Tail,
			})
			if err != nil {
				contextkey.ValueSlogLoggerDefault(ctx).Warn("compose logs: open reader failed",
					"project", project,
					"command", name,
					"id", id,
					"error", err,
				)
				addErr(fmt.Errorf("open logs for command %q (%s): %w", name, id, err))
				return nil // aggregate, don't short-circuit
			}
			defer func() {
				if err := reader.Close(); err != nil {
					addErr(fmt.Errorf("close logs for command %q (%s): %w", name, id, err))
				}
			}()

			for rec := range reader.Records() {
				if rec.Err != nil {
					contextkey.ValueSlogLoggerDefault(ctx).Warn("compose logs: record error",
						"project", project,
						"command", name,
						"error", rec.Err,
					)
					addErr(fmt.Errorf("read logs for command %q: %w", name, rec.Err))
					continue
				}
				select {
				case out <- LogMessage{Command: name, Record: rec}:
				case <-egCtx.Done():
					return nil
				}
			}
			return nil
		})
	}

	return errors.Join(eg.Wait(), errors.Join(errs...))
}
