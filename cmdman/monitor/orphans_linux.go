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

const (
	// orphanGrace is how long a process the command left behind has to exit on
	// SIGTERM before it is killed outright.
	orphanGrace = 2 * time.Second
	// sweepBound caps the whole sweep. A process sitting in an uninterruptible
	// wait ignores SIGKILL too, and without a cap the run would hang on it the
	// same way it would hang on a pty reader that never wakes.
	sweepBound = 10 * time.Second
	// sweepPoll is how often a signalled process is looked at again.
	sweepPoll = 20 * time.Millisecond
)

// becomeSubreaper makes this process the reaper of its whole descendant tree: a
// process whose own parent is gone is reparented here instead of to init, so
// its parent id becomes the monitor's own pid. That is what lets a run find
// what its command left in the command's own session and take it down, instead
// of handing those leftovers to init where they would go on holding the port,
// the log file or the pty they were given.
func becomeSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// sweepOptions are the parts of the sweep a test replaces. The monitor runs
// with defaultSweepOptions.
type sweepOptions struct {
	grace time.Duration
	kill  func(pid int, sig syscall.Signal) error
}

func defaultSweepOptions() sweepOptions {
	return sweepOptions{grace: orphanGrace, kill: unix.Kill}
}

// sweepRunSurvivors terminates and reaps the processes the finished run left in
// the command's own session and returns how many were still alive when it gave
// up. pgid is the process group the command led, which is also its session id
// because the command is its own session leader. A process that made a session
// of its own - a deliberately detached daemon that must outlive the run, or a
// grandchild that broke away - is left alone, the same as on a non-Linux host.
// ctx carries the bound: the sweep keeps going until nothing is left or ctx
// expires.
func sweepRunSurvivors(ctx context.Context, logger *slog.Logger, pgid int) int {
	return sweepRunSurvivorsWith(ctx, logger, pgid, defaultSweepOptions())
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

	// One group-wide signal first: everything the command spawned that stayed
	// in its process group hears it at once, including processes the scan below
	// cannot see yet because their own parent is still alive.
	_ = opts.kill(-pgid, syscall.SIGTERM)

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
		if ctx.Err() != nil {
			return len(alive)
		}

		signalAll(alive, syscall.SIGTERM, opts.kill)
		alive = waitGone(ctx, alive, opts.grace)
		if len(alive) > 0 {
			signalAll(alive, syscall.SIGKILL, opts.kill)
			alive = waitGone(ctx, alive, opts.grace)
		}
		if len(alive) > 0 && ctx.Err() != nil {
			return len(alive)
		}
		// Killing a process reparents its own children here in turn, so the
		// scan runs again instead of the sweep stopping at the first layer.
	}
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
	session int
}

// readProcIDs reads pid's parent and session ids. The comm field sits in
// parentheses and may contain spaces and parentheses of its own, so the
// fixed-position fields that follow it - state, ppid, pgrp, session - are
// counted from the last ')' rather than from the start of the line.
func readProcIDs(pid int) (procIDs, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procIDs{}, err
	}
	stat := string(b)
	commEnd := strings.LastIndex(stat, ")")
	if commEnd < 0 {
		return procIDs{}, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(stat[commEnd+1:])
	if len(fields) < 4 {
		return procIDs{}, fmt.Errorf("too few fields in /proc/%d/stat", pid)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return procIDs{}, fmt.Errorf("parse ppid of /proc/%d/stat: %w", pid, err)
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return procIDs{}, fmt.Errorf("parse session of /proc/%d/stat: %w", pid, err)
	}
	return procIDs{ppid: ppid, session: session}, nil
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
	deadline := time.NewTimer(grace)
	defer deadline.Stop()
	tick := time.NewTicker(sweepPoll)
	defer tick.Stop()
	for {
		pids = reapGone(pids)
		if len(pids) == 0 {
			return nil
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

func signalAll(pids []int, sig syscall.Signal, kill func(pid int, sig syscall.Signal) error) {
	for _, pid := range pids {
		_ = kill(pid, sig)
	}
}
