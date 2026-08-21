package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

const (
	// muxOpLivenessInterval is how often the follower asks whether the worker's
	// record is still there while waiting for its exit status.
	muxOpLivenessInterval = 2 * time.Second
	// muxOpExitGrace is how long the follower keeps waiting for an exit event
	// after the worker's record has gone. The monitor appends the event before
	// it removes the record, so the two cross paths; past the grace period the
	// event is not coming.
	muxOpExitGrace = 5 * time.Second
)

// muxOpLogEnd returns where an operation's log stands before the run about to
// start adds to it.
//
// One window's history lives in one file: an identity can be operated on again,
// and a second run appends to what the first left rather than replacing it. So
// the follower is told where the run it is following begins, or it prints every
// earlier run alongside — a failed run's error read back as if the run that
// succeeded had reported it.
//
// The boundary is a position rather than a moment deliberately: a host clock can
// step backwards between two runs, and a boundary in time would then either hide
// this run's output or let the previous run's through.
func muxOpLogEnd(ctx context.Context, logPath string) k8sfile.Offset {
	end, err := k8sfile.CurrentEnd("", map[string]string{logdriver.LogOptPath: logPath})
	if err != nil {
		// Where the run begins is then unknown, and everything retained is
		// printed: an extra run's output on screen says more than none at all.
		contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
			ctx, "mux op: read where the worker's log stands", "path", logPath, "error", err,
		)
		return k8sfile.Offset{}
	}
	return end
}

// followMuxOpOutput prints the worker's output as it arrives and returns when
// the worker stops producing any. from is where the log stood before the worker
// was registered, so what earlier runs left in it is not printed again.
//
// Nothing here decides whether the operation succeeded — that is the exit
// event's job — so a stream that ends early is not reported as a failure. The
// ordinary reason for one is the worker finishing: auto-remove takes its record
// away, and the follow plumbing reads that record. When the record is already
// gone by the time following starts, the log file is replayed instead; it lives
// in the runtime dir rather than the removed command dir precisely so that it
// still can be.
func followMuxOpOutput(
	ctx context.Context,
	opts MuxOpOptions,
	id, logPath string,
	from k8sfile.Offset,
) {
	r, err := opts.Svc.Logs(ctx, cmdman.LogsRequest{
		IDOrName:    id,
		Follow:      true,
		StartOffset: from,
	})
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).DebugContext(
			ctx, "mux op: follow worker output", "error", err,
		)
		replayMuxOpLog(ctx, opts, logPath, from)
		return
	}
	defer r.Close()
	_ = RenderLogs(opts.Stdout, opts.Stderr, r.Records())
}

// replayMuxOpLog prints what the worker wrote, reading its log file directly and
// from the same run boundary the live path uses.
func replayMuxOpLog(ctx context.Context, opts MuxOpOptions, logPath string, from k8sfile.Offset) {
	r, err := logdriver.NewReader(
		ctx,
		string(logdriver.DriverK8sFile),
		"",
		map[string]string{
			logdriver.LogOptPath:    logPath,
			logdriver.LogOptMaxFile: muxOpLogMaxFile,
		},
		logdriver.ReaderOption{StartOffset: from},
	)
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx, "mux op: read worker output", "path", logPath, "error", err,
		)
		return
	}
	defer r.Close()
	_ = RenderLogs(opts.Stdout, opts.Stderr, r.Records())
}

// waitMuxOpExit reports what the worker exited with.
//
// A worker that was killed outright never appends an exit event, so waiting for
// one and nothing else would hang for good. Alongside the event, the store is
// asked whether the worker's record is still there; listing is what notices a
// monitor that died. Once the record is gone the event is given a short grace
// period to turn up — the monitor appends it just before removing the record,
// so on a normal exit the two are moments apart — and after that the worker is
// declared dead rather than waited on.
func waitMuxOpExit(
	ctx context.Context,
	svc *cmdman.Service,
	sub *cmdman.EventsSubscription,
	id string,
) error {
	return waitMuxOpExitWithin(ctx, svc, sub, id, muxOpLivenessInterval, muxOpExitGrace)
}

// waitMuxOpExitWithin is [waitMuxOpExit] with its two waits spelled out rather
// than taken from the constants, so a test can drive them faster than an
// operation would.
func waitMuxOpExitWithin(
	ctx context.Context,
	svc *cmdman.Service,
	sub *cmdman.EventsSubscription,
	id string,
	interval, grace time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var graceUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case rec, ok := <-sub.Records():
			if !ok {
				return errors.New("mux op: stopped watching before the worker reported an exit")
			}
			if rec.Err != nil {
				continue
			}
			switch rec.Event.Type {
			case model.EventTypeExited:
				if rec.Event.ExitCode == nil || *rec.Event.ExitCode == 0 {
					return nil
				}
				return &ExitCodeError{Code: *rec.Event.ExitCode}
			case model.EventTypeFailed:
				if rec.Event.Error != "" {
					return fmt.Errorf("mux op worker failed: %s", rec.Event.Error)
				}
				return errors.New("mux op worker failed")
			}
		case <-ticker.C:
			// A store that cannot be read right now says nothing about the
			// worker, so it counts as alive: the price of guessing wrong here
			// is abandoning an operation that is still going.
			present, err := findCommandByID(ctx, svc, id)
			if err != nil || present {
				graceUntil = time.Time{}
				continue
			}
			if graceUntil.IsZero() {
				graceUntil = time.Now().Add(grace)
				continue
			}
			if time.Now().After(graceUntil) {
				return errors.New("mux op worker died without reporting an exit")
			}
		}
	}
}

// findCommandByID reports whether a record for id is still registered.
func findCommandByID(ctx context.Context, svc *cmdman.Service, id string) (bool, error) {
	entries, err := svc.List(ctx, cmdman.ListRequest{AllStates: true})
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(entries, func(e store.CommandEntry) bool {
		return e.ID == id
	}), nil
}
