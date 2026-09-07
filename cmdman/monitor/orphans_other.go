//go:build !linux && !plan9 && !windows && !wasm

package monitor

import (
	"context"
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
func sweepRunSurvivors(ctx context.Context, logger *slog.Logger, pgid int) int {
	if pgid <= 0 {
		return 0
	}
	logger.DebugContext(ctx, "signalling the process group the run leaves behind",
		slog.Int("pgid", pgid),
	)
	_ = signalProcessGroup(pgid, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case <-time.After(orphanGrace):
	}
	_ = signalProcessGroup(pgid, syscall.SIGKILL)
	return 0
}
