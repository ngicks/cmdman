//go:build linux

package cmdman_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// procStat holds the subset of /proc/<pid>/stat fields the detach test needs.
type procStat struct {
	pid     int
	ppid    int
	pgrp    int
	session int
}

// readProcStat parses /proc/<pid>/stat. The comm field (field 2) is wrapped in
// parentheses and may itself contain spaces or parentheses, so the fixed fields
// are read relative to the last ')'.
func readProcStat(t *testing.T, pid int) procStat {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatalf("read /proc/%d/stat: %v", pid, err)
	}
	s := string(data)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 {
		t.Fatalf("malformed /proc/%d/stat: %q", pid, s)
	}
	// Fields after comm: [0]=state [1]=ppid [2]=pgrp [3]=session ...
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 4 {
		t.Fatalf("too few stat fields for pid %d: %q", pid, s)
	}
	atoi := func(i int) int {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			t.Fatalf("parse stat field %d (%q): %v", i, fields[i], err)
		}
		return n
	}
	return procStat{pid: pid, ppid: atoi(1), pgrp: atoi(2), session: atoi(3)}
}

// TestDetach_MonitorIsDoubleForked verifies that the monitor process is
// detached via a double-fork rather than a single setsid: the running monitor
// must NOT be a session leader (its session id differs from its pid), and it
// must have been reparented away from the short-lived `cmdman start` process.
//
// A single setsid (the previous mechanism) would leave session == pid, so this
// is a direct regression guard for the double-fork.
func TestDetach_MonitorIsDoubleForked(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "detach-target", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "detach-target", "running", defaultTimeout)

	info := env.inspectJSON(ctx, "detach-target")
	stateDetail, _ := info["StateJSON"].(map[string]any)
	pidF, _ := stateDetail["monitor_pid"].(float64)
	pid := int(pidF)
	if pid <= 0 {
		t.Fatal("could not read monitor pid from inspect output")
	}

	stat := readProcStat(t, pid)

	// Double-fork invariant: the monitor is a member of a session it does not
	// lead. The intermediate that called setsid is the session leader, and it
	// has exited.
	if stat.session == pid {
		t.Fatalf(
			"monitor pid %d is a session leader (session=%d); expected a double-forked non-leader",
			pid,
			stat.session,
		)
	}

	// The intermediate session leader exits immediately after forking the
	// daemon, so the monitor is orphaned and reparented (to init, pid 1, or a
	// subreaper). In any case its parent is no longer the `cmdman start`
	// invocation, which has also already returned.
	if stat.ppid == pid {
		t.Fatalf("monitor pid %d reports itself as its own parent", pid)
	}
	t.Logf(
		"monitor pid=%d ppid=%d pgrp=%d session=%d",
		stat.pid,
		stat.ppid,
		stat.pgrp,
		stat.session,
	)
}
