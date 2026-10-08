//go:build !linux && !plan9 && !windows && !wasm

package monitor

import (
	"context"
	"errors"
	"log/slog"
	"syscall"
	"time"
)

// sweepPoll is how often the process group is checked for whether anything
// still answers, so a run whose leftovers exit promptly on SIGTERM does not pay
// the whole grace. The Linux path names its own; this build cannot see it.
const sweepPoll = 20 * time.Millisecond

// becomeSubreaper does nothing here. PR_SET_CHILD_SUBREAPER is a Linux
// facility, so a process the command leaves behind is reparented to init and
// the monitor can neither enumerate nor reap it.
func becomeSubreaper() error {
	return nil
}

// sweepOptions are the parts of the sweep and the await a test replaces. The
// monitor runs with defaultSweepOptions.
type sweepOptions struct {
	grace time.Duration
	// bound caps the await once the stop's SIGKILL has gone out. The await
	// drops the cancellation of the ctx it is given at that point, and with it
	// any deadline, so the bound cannot ride on ctx.
	bound time.Duration
	// stopRequested reports whether a stop has been asked for. The terminating
	// sweep checks it before every signal it would send and hands over once it
	// reports true. nil never reports a stop.
	stopRequested func() bool
}

func defaultSweepOptions() sweepOptions {
	return sweepOptions{grace: orphanGrace, bound: sweepBound}
}

func (o sweepOptions) stopping() bool {
	return o.stopRequested != nil && o.stopRequested()
}

// sweepRunSurvivors signals the process group the command led. The group is the
// only handle this build has on what the run left behind: the monitor is not a
// subreaper here and there is no /proc to scan, so it cannot enumerate the
// leftovers and signal each one by pid the way the Linux build does. The cost
// is the one the Linux build avoids: a helper that a process still handling a
// stop signal forked into the group is signalled too. A process that broke
// away into a session of its own survives unseen. It reaps nothing, so it
// always reports zero left behind rather than a count it cannot establish.
//
// It sends SIGTERM only once a probe shows the group still has a member, and it
// returns as soon as the group is empty rather than always waiting out the
// grace, so a run whose leftovers exit promptly on SIGTERM does not pay the
// full delay at every run end. Only a group that is still alive when the grace
// (or the sweep bound carried on ctx) runs out is escalated to SIGKILL.
//
// stopRequested is checked before each group signal the sweep would send and on
// every poll. Once it reports true the sweep sends nothing further and returns
// sweepHandedOver: the group belongs to the stop, which awaitRunSurvivors waits
// out.
func sweepRunSurvivors(
	ctx context.Context,
	logger *slog.Logger,
	pgid int,
	stopRequested func() bool,
) int {
	opts := defaultSweepOptions()
	opts.stopRequested = stopRequested
	return sweepRunSurvivorsWith(ctx, logger, pgid, opts)
}

func sweepRunSurvivorsWith(
	ctx context.Context,
	logger *slog.Logger,
	pgid int,
	opts sweepOptions,
) int {
	if pgid <= 0 {
		return 0
	}
	if !groupHasMember(pgid) {
		// Nothing outlived the run, so there is nothing to signal, wait on or
		// kill.
		return 0
	}
	if opts.stopping() {
		return sweepHandedOver
	}
	logger.DebugContext(ctx, "signalling the process group the run leaves behind",
		slog.Int("pgid", pgid),
	)
	_ = signalProcessGroup(pgid, syscall.SIGTERM)

	deadline := time.NewTimer(opts.grace)
	defer deadline.Stop()
	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		if !groupHasMember(pgid) {
			return 0
		}
		if opts.stopping() {
			return sweepHandedOver
		}
		select {
		case <-ctx.Done():
		case <-deadline.C:
		case <-tick.C:
			continue
		}
		if opts.stopping() {
			return sweepHandedOver
		}
		_ = signalProcessGroup(pgid, syscall.SIGKILL)
		return 0
	}
}

// awaitRunSurvivors waits out the process group the command led while a stop of
// that command is in progress, with the options the monitor runs with.
func awaitRunSurvivors(
	ctx context.Context,
	logger *slog.Logger,
	pgid int,
	killed func() bool,
) int {
	return awaitRunSurvivorsWith(ctx, logger, pgid, killed, defaultSweepOptions())
}

// awaitRunSurvivorsWith polls the process group until it is empty or ctx is
// done, and sends nothing: the stop already delivered its signal to the group,
// and a second copy of it is what breaks a process still carrying the stop out.
// Once killed reports that the stop's own SIGKILL has gone out, the group has
// had the only escalation this build can deliver - it cannot reach a survivor
// that left the group - so the poll only gets a bound of its own, opts.bound,
// that ignores ctx's cancellation. Like the sweep it cannot count what is left,
// so it always reports zero.
func awaitRunSurvivorsWith(
	ctx context.Context,
	logger *slog.Logger,
	pgid int,
	killed func() bool,
	opts sweepOptions,
) int {
	if pgid <= 0 {
		return 0
	}
	logger.DebugContext(ctx, "waiting out the process group a stop is taking down",
		slog.Int("pgid", pgid),
	)

	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		if !groupHasMember(pgid) {
			return 0
		}
		if killed() {
			break
		}
		select {
		case <-ctx.Done():
			return 0
		case <-tick.C:
		}
	}

	boundCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.bound)
	defer cancel()
	for {
		if !groupHasMember(pgid) {
			return 0
		}
		select {
		case <-boundCtx.Done():
			return 0
		case <-tick.C:
		}
	}
}

// groupHasMember reports whether the process group led by pgid still has a
// member the monitor may signal. Signal 0 delivers nothing; it only probes.
func groupHasMember(pgid int) bool {
	return !errors.Is(signalProcessGroup(pgid, 0), syscall.ESRCH)
}
