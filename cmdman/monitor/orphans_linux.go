//go:build linux

package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// sweepPoll is how often a signalled process is looked at again.
const sweepPoll = 20 * time.Millisecond

// becomeSubreaper makes this process the reaper of its whole descendant tree: a
// process whose own parent is gone is reparented here instead of to init, so
// its parent id becomes the monitor's own pid. That is what lets a run find
// what its command left in the command's own session and take it down, instead
// of handing those leftovers to init where they would go on holding the port,
// the log file or the pty they were given.
func becomeSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// sweepOptions are the parts of the sweep and the await a test replaces. The
// monitor runs with defaultSweepOptions.
type sweepOptions struct {
	grace time.Duration
	// bound caps the phase of the await that completes a stop's SIGKILL. That
	// phase drops the cancellation of the ctx it is given, and with it any
	// deadline, so its bound cannot ride on ctx the way the terminating sweep's
	// does.
	bound time.Duration
	kill  func(pid int, sig syscall.Signal) error
	// stopRequested reports whether a stop has been asked for. The terminating
	// sweep checks it before every signal it would send and hands over once it
	// reports true. nil never reports a stop.
	stopRequested func() bool
	// reached is called each time the await's SIGKILL by pid reaches a process.
	// nil reports nothing.
	reached func()
}

func defaultSweepOptions() sweepOptions {
	return sweepOptions{grace: orphanGrace, bound: sweepBound, kill: unix.Kill}
}

func (o sweepOptions) stopping() bool {
	return o.stopRequested != nil && o.stopRequested()
}

func (o sweepOptions) report() {
	if o.reached != nil {
		o.reached()
	}
}

// sweepRunSurvivors terminates and reaps the processes the finished run left in
// the command's own session and returns how many were still alive when it gave
// up. pgid is the process group the command led, which is also its session id
// because the command is its own session leader. A process that made a session
// of its own - a deliberately detached daemon that must outlive the run, or a
// grandchild that broke away - is left alone, the same as on a non-Linux host.
// ctx carries the bound: the sweep keeps going until nothing is left or ctx
// expires.
//
// stopRequested is checked before every signal the sweep would send. Once it
// reports true the sweep sends nothing further and returns sweepHandedOver:
// what is left belongs to the stop, which awaitRunSurvivors waits out.
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
	// pgid is the command's session id: the command leads its own session, so
	// its session id equals its pid, which is the pgid passed in. Without it the
	// scan below has nothing to match against, so there is nothing to sweep.
	if pgid <= 0 {
		return 0
	}
	self := os.Getpid()

	// The sweep never signals the command's process group. A run that ends on a
	// stop has already had that signal delivered to the group, and a process
	// still handling it may have forked a helper into the same group to do the
	// work: podman runs `crun kill` that way, and when that helper is killed
	// mid-way podman exits without cleaning up its container. The scan below
	// only finds processes already reparented to the monitor, so a helper whose
	// parent is still alive is left alone until that parent is gone, and the
	// rescan then reaches it by pid like any other survivor.
	for {
		found, err := runSurvivors(self, pgid)
		if err != nil {
			logger.WarnContext(ctx, "sweep: scan processes", slog.String("error", err.Error()))
			return 0
		}
		alive := reapGone(found)
		if len(alive) == 0 {
			return 0
		}
		// A stop that lands while the sweep runs takes over what is left, even
		// once the sweep is past its own bound: the stop's timeout decides when
		// that gets killed, not the sweep's.
		if opts.stopping() {
			return sweepHandedOver
		}
		if ctx.Err() != nil {
			return len(alive)
		}

		if !signalEach(alive, syscall.SIGTERM, opts.kill, opts.stopping) {
			return sweepHandedOver
		}
		alive = waitGoneUntil(ctx, alive, opts.grace, opts.stopping)
		if len(alive) > 0 {
			if !signalEach(alive, syscall.SIGKILL, opts.kill, opts.stopping) {
				return sweepHandedOver
			}
			waitGoneUntil(ctx, alive, opts.grace, opts.stopping)
		}
		// Killing a process reparents its own children here in turn, so the
		// scan runs again instead of the sweep stopping at the first layer. The
		// rescan also counts what the waits above left alive, and the top of the
		// loop is where a stop or the bound that cut a wait short ends the
		// sweep.
	}
}

// awaitRunSurvivors waits out what the finished run left in the command's own
// session while a stop of that command is in progress, with the options the
// monitor runs with. reached is called each time the stop's SIGKILL, carried on
// by pid, reaches a survivor.
func awaitRunSurvivors(
	ctx context.Context,
	logger *slog.Logger,
	pgid int,
	killed func() bool,
	reached func(),
) int {
	opts := defaultSweepOptions()
	opts.reached = reached
	return awaitRunSurvivorsWith(ctx, logger, pgid, killed, opts)
}

// awaitRunSurvivorsWith reaps what the run left in the command's own session as
// it exits and returns 0 once nothing is left, or the number still alive when
// ctx is done. It sends no signal of its own.
//
// The stop has already delivered its signal to the command's whole process
// group once. A process still alive in the session is most likely the one
// carrying that stop out - podman waiting for its container to go down after
// the wrapper shell that started it died first - and a second copy of the same
// signal by pid is exactly what broke that: it lands on the helper podman
// forked to do the work, the helper dies mid-way, and the container is never
// cleaned up. So the await leaves the survivors to the stop and only reaps.
//
// killed reports whether the stop's own SIGKILL has gone out, from the client
// or from the deadline the monitor armed for the stop. It is checked on entry
// and on every poll; once it reports true the await completes that SIGKILL and
// returns what it could not take down within opts.bound.
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
	self := os.Getpid()

	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		found, err := runSurvivors(self, pgid)
		if err != nil {
			logger.WarnContext(ctx, "await: scan processes", slog.String("error", err.Error()))
			return 0
		}
		alive := reapGone(found)
		if len(alive) == 0 {
			return 0
		}
		if killed() {
			return completeStopKill(ctx, logger, self, pgid, opts)
		}
		select {
		case <-ctx.Done():
			return len(alive)
		case <-tick.C:
		}
	}
}

// completeStopKill carries a stop's SIGKILL to the survivors it could not
// reach. The stop sent it to the command's process group, which takes every
// member of the group wherever it sits in the tree, so a member is only waited
// for here. A process that moved into a process group of its own is outside
// that signal's reach, so it is killed by pid instead. Such a process becomes
// visible here only once its parent is gone and it is reparented to the
// monitor, which is why the scan is repeated until the session is empty or
// opts.bound runs out.
//
// Every SIGKILL by pid that reaches a process goes to opts.reached. The group
// may have had nobody left for the stop's own SIGKILL, and then this one is
// what ends the run's survivors.
//
// The bound is its own and ignores ctx's cancellation: a monitor shutting down
// still has to finish the kill it already committed to, and a process in an
// uninterruptible wait ignores SIGKILL, so the run has to end regardless.
func completeStopKill(
	ctx context.Context,
	logger *slog.Logger,
	self, pgid int,
	opts sweepOptions,
) int {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opts.bound)
	defer cancel()
	for {
		found, err := runSurvivors(self, pgid)
		if err != nil {
			logger.WarnContext(ctx, "await: scan processes", slog.String("error", err.Error()))
			return 0
		}
		alive := reapGone(found)
		if len(alive) == 0 {
			return 0
		}
		if ctx.Err() != nil {
			return len(alive)
		}
		for _, pid := range outsideGroup(alive, pgid) {
			if opts.kill(pid, syscall.SIGKILL) == nil {
				opts.report()
			}
		}
		alive = waitGone(ctx, alive, opts.grace)
		if len(alive) > 0 && ctx.Err() != nil {
			return len(alive)
		}
	}
}

// outsideGroup returns the pids whose process group is not pgid. Every pid
// passed in is an unreaped child of the monitor, so none of them can have been
// handed out again to another process by the time its stat is read.
func outsideGroup(pids []int, pgid int) []int {
	var out []int
	for _, pid := range pids {
		ids, err := readProcIDs(pid)
		if err != nil {
			// Gone since the scan.
			continue
		}
		if ids.pgrp != pgid {
			out = append(out, pid)
		}
	}
	return out
}

// runSurvivors lists the monitor's direct children that are still in the
// command's own session, which is what the finished run left behind. The
// supervised command leads a session of its own - its session id equals its
// pid, the pgid passed in - and everything it spawns inherits that session. A
// process can create a session but never join one, so anything that made a
// session of its own falls outside the match and is left alone: a deliberately
// detached daemon such as a shared multiplexer server the run must not tear
// down, or a grandchild that broke away and reverts to the same accepted,
// unreaped cost as on a non-Linux host. A hook keeps the monitor's session, so
// it never matches either.
//
// /proc is scanned rather than read from /proc/<pid>/task/<tid>/children
// because that file only exists when the kernel was built with
// CONFIG_PROC_CHILDREN.
func runSurvivors(self, pgid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			// Not a process directory.
			continue
		}
		ids, err := readProcIDs(pid)
		if err != nil {
			// The process ended between the listing and the read.
			continue
		}
		if ids.ppid != self || ids.session != pgid {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// procIDs are the identity fields of /proc/<pid>/stat the sweep reads.
type procIDs struct {
	ppid    int
	pgrp    int
	session int
}

// readProcIDs reads pid's parent, process group and session ids.
func readProcIDs(pid int) (procIDs, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procIDs{}, err
	}
	ids, err := parseProcIDs(string(b))
	if err != nil {
		return procIDs{}, fmt.Errorf("/proc/%d/stat: %w", pid, err)
	}
	return ids, nil
}

// parseProcIDs reads the identity fields out of a /proc/<pid>/stat line. The
// comm field sits in parentheses and may contain spaces and parentheses of its
// own, so the fixed-position fields that follow it - state, ppid, pgrp,
// session - are counted from the last ')' rather than from the start of the
// line.
func parseProcIDs(stat string) (procIDs, error) {
	commEnd := strings.LastIndex(stat, ")")
	if commEnd < 0 {
		return procIDs{}, errors.New("malformed: no end of comm")
	}
	fields := strings.Fields(stat[commEnd+1:])
	if len(fields) < 4 {
		return procIDs{}, errors.New("too few fields")
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procIDs{}, fmt.Errorf("parse ppid: %w", err)
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return procIDs{}, fmt.Errorf("parse pgrp: %w", err)
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return procIDs{}, fmt.Errorf("parse session: %w", err)
	}
	return procIDs{ppid: ppid, pgrp: pgrp, session: session}, nil
}

// reapGone waits on each pid without blocking, so a process that has already
// exited is reaped here instead of piling up as a zombie child of the monitor,
// and returns the ones still running. Every wait names one pid: waiting on any
// child would steal the exit status of a hook exec.Cmd running at the same
// time.
func reapGone(pids []int) []int {
	alive := make([]int, 0, len(pids))
	for _, pid := range pids {
		var ws unix.WaitStatus
		wpid, err := unix.Wait4(pid, &ws, unix.WNOHANG, nil)
		switch {
		case wpid == pid:
			// Exited and now reaped.
		case err == nil, errors.Is(err, unix.EINTR):
			// wpid is 0 for a process that has not exited; an interrupted wait
			// says nothing either way, so both are looked at again.
			alive = append(alive, pid)
		default:
			// ECHILD and the like: nothing of the monitor's is left under this
			// pid.
		}
	}
	return alive
}

// waitGone polls pids until each is gone or grace has passed and returns those
// still alive. ctx cuts it short, so the caller's bound holds even when every
// round runs its grace out.
func waitGone(ctx context.Context, pids []int, grace time.Duration) []int {
	return waitGoneUntil(ctx, pids, grace, nil)
}

// waitGoneUntil is waitGone that also returns once halt reports true, so a stop
// landing mid-wait hands the survivors over at once instead of after the rest
// of the grace. A nil halt never reports true.
func waitGoneUntil(
	ctx context.Context,
	pids []int,
	grace time.Duration,
	halt func() bool,
) []int {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		pids = reapGone(pids)
		if len(pids) == 0 {
			return nil
		}
		if halt != nil && halt() {
			return pids
		}
		select {
		case <-ctx.Done():
			return pids
		case <-deadline.C:
			return pids
		case <-tick.C:
		}
	}
}

// signalEach sends sig to pids one at a time and checks halt before each, so a
// stop landing partway through the list gets nothing further from the sweep. It
// reports whether it got through the whole list.
func signalEach(
	pids []int,
	sig syscall.Signal,
	kill func(pid int, sig syscall.Signal) error,
	halt func() bool,
) bool {
	for _, pid := range pids {
		if halt() {
			return false
		}
		_ = kill(pid, sig)
	}
	return true
}
