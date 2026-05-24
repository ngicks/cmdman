package compose

import (
	"context"
	"errors"
	"fmt"
	"iter"
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
	// It is ignored when Follow is set, where the stock/live boundary is "now".
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
// Output is produced in two phases. The stock phase reads each command's stored
// records up to "now", merges them across commands in timestamp order, and
// emits them; because stored logs are finite they can be safely reordered. When
// Follow=true a live phase then tails each command from "now" onward and emits
// records as they arrive — live records cannot be reordered across commands, so
// only the stock prefix is timestamp-ordered.
//
// Per resolved-decision 15, an empty project target set ends with a nil error.
// Per resolved-decision 21, errors are aggregated and all commands are
// attempted.
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
			Labels:    projectLabels(selection.WorkDir, selection.Project),
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

		// "now" splits finite stored logs (the stock, reorderable) from the live
		// tail (not reorderable). Capture it once so the two phases share a
		// boundary with no gap and no overlap.
		now := time.Now()
		stockUntil := opts.Until
		if opts.Follow {
			stockUntil = now
		}

		var errs []error
		if err := s.logsStock(ctx, selection.Project, entries, opts, stockUntil, out); err != nil {
			errs = append(errs, err)
		}
		// Since is inclusive, so start the live tail one tick past "now" to avoid
		// re-emitting a record that landed exactly at the boundary.
		if opts.Follow && ctx.Err() == nil {
			if err := s.logsLive(ctx, selection.Project, entries, now.Add(time.Nanosecond), out); err != nil {
				errs = append(errs, err)
			}
		}
		errc <- errors.Join(errs...)
	}()

	return out, errc
}

// logsStock reads each command's stored records up to until, adapts every
// reader's record channel into a timestamp-ordered iterator, and merges them
// across commands with hiter.MergeFunc. Each command's stored records are
// already time-ordered, so a streaming k-way merge suffices — no full
// in-memory sort. Open/read/close errors are aggregated and returned.
func (s *Service) logsStock(
	ctx context.Context,
	project string,
	entries []cmdmanEntry,
	opts LogsOption,
	until time.Time,
	out chan<- LogMessage,
) (retErr error) {
	type namedRecord struct {
		cmdName string
		rec     logdriver.Record
	}

	var (
		errs    []error
		seqs    []iter.Seq[namedRecord]
		readers []logdriver.Reader
	)

	// Open one non-follow reader per command. Readers stay open until the merge
	// below drains them, so they are closed in a deferred sweep rather than per
	// command.
	for _, entry := range entries {
		cmdName := ""
		if entry.ConfigJSON != nil {
			cmdName = entry.ConfigJSON.Labels[LabelCommand]
		}

		reader, err := s.svc.Logs(ctx, cmdman.LogsRequest{
			IDOrName: entry.ID,
			Follow:   false,
			Since:    opts.Since,
			Until:    until,
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
		readers = append(readers, reader)
		seqs = append(seqs, hiter.Map(
			func(rec logdriver.Record) namedRecord {
				return namedRecord{cmdName: cmdName, rec: rec}
			},
			hiter.Chan(ctx, reader.Records()),
		))
	}

	defer func() {
		for _, reader := range readers {
			if err := reader.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close logs: %w", err))
			}
		}
	}()

	if len(seqs) == 0 {
		return errors.Join(errs...)
	}

	cmp := func(a, b namedRecord) int {
		return a.rec.Line.Time.Compare(b.rec.Line.Time)
	}

	// Fold the per-command iterators into one timestamp-ordered sequence. The
	// inputs are already sorted, so MergeFunc reorders across commands lazily.
	merged := seqs[0]
	for _, seq := range seqs[1:] {
		merged = hiter.MergeFunc(cmp, merged, seq)
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

// logsLive opens per-command follow readers concurrently from since and sends
// records on out as they arrive. Cross-command ordering is not stabilized: live
// records cannot be reordered. All goroutines are attempted; errors are
// aggregated.
func (s *Service) logsLive(
	ctx context.Context,
	project string,
	entries []cmdmanEntry,
	since time.Time,
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
				Since:    since,
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
