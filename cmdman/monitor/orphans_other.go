//go:build !linux && !plan9 && !windows && !wasm

package monitor

import (
	"context"
	"errors"
	"log/slog"
	"syscall"
	"time"
)

const (
	// orphanGrace is how long a process the command left behind has to exit on
	// SIGTERM before it is killed outright.
	orphanGrace = 2 * time.Second
	// sweepBound caps the whole sweep, so a process that refuses to die cannot
	// keep the run from finishing.
	sweepBound = 10 * time.Second
	// sweepPoll is how often the process group is checked for whether anything
	// still answers, so a run whose leftovers exit promptly on SIGTERM does not
	// pay the whole grace. The Linux path names its own; this build cannot see
	// it.
	sweepPoll = 20 * time.Millisecond
)

// becomeSubreaper does nothing here. PR_SET_CHILD_SUBREAPER is a Linux
// facility, so a process the command leaves behind is reparented to init and
// the monitor can neither enumerate nor reap it.
func becomeSubreaper() error {
	return nil
}

// sweepRunSurvivors signals the process group the command led, which is as far
// as the monitor can reach without being the reaper of its descendants: a
// process that broke away into a session of its own survives unseen. It reaps
// nothing, so it always reports zero left behind rather than a count it cannot
// establish.
//
// It returns as soon as the group is empty rather than always waiting out the
// grace, so a run whose leftovers exit promptly on SIGTERM does not pay the
// full delay at every run end. Only a group that is still alive when the grace
// (or the sweep bound carried on ctx) runs out is escalated to SIGKILL.
func sweepRunSurvivors(ctx context.Context, logger *slog.Logger, pgid int) int {
	if pgid <= 0 {
		return 0
	}
	logger.DebugContext(ctx, "signalling the process group the run leaves behind",
		slog.Int("pgid", pgid),
	)
	if err := signalProcessGroup(pgid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		// The group has no members: nothing outlived the run, so there is
		// nothing to wait on or kill.
		return 0
	}

	deadline := time.NewTimer(orphanGrace)
	defer deadline.Stop()
	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		// Signal 0 delivers nothing; it only reports whether the group still has
		// a member the monitor may signal.
		if err := signalProcessGroup(pgid, 0); errors.Is(err, syscall.ESRCH) {
			return 0
		}
		select {
		case <-ctx.Done():
			_ = signalProcessGroup(pgid, syscall.SIGKILL)
			return 0
		case <-deadline.C:
			_ = signalProcessGroup(pgid, syscall.SIGKILL)
			return 0
		case <-tick.C:
		}
	}
}
