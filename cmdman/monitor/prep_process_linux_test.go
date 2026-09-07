//go:build linux

package monitor

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// procStat holds the /proc/<pid>/stat fields this test compares.
type procStat struct {
	pid     int
	pgrp    int
	session int
}

// parseProcStat reads pgrp and session out of a /proc/<pid>/stat line. The comm
// field is parenthesized and may itself contain spaces and parentheses, so the
// fixed-position fields are counted from the last ')' rather than from the
// start of the line.
func parseProcStat(t *testing.T, s string) procStat {
	t.Helper()
	pidStr, rest, ok := strings.Cut(s, " ")
	assert.Assert(t, ok, "malformed stat line: %q", s)
	pid, err := strconv.Atoi(pidStr)
	assert.NilError(t, err)

	commEnd := strings.LastIndex(rest, ")")
	assert.Assert(t, commEnd >= 0, "malformed stat line: %q", s)
	// After comm come: state, ppid, pgrp, session, ...
	fields := strings.Fields(rest[commEnd+1:])
	assert.Assert(t, len(fields) >= 4, "too few stat fields: %q", s)
	pgrp, err := strconv.Atoi(fields[2])
	assert.NilError(t, err)
	session, err := strconv.Atoi(fields[3])
	assert.NilError(t, err)
	return procStat{pid: pid, pgrp: pgrp, session: session}
}

// runCatStat spawns `cat /proc/self/stat` with prep applied and returns the
// child's own view of its ids. cat is exec'd directly so the pid in the stat
// line is the pid the prep function configured.
func runCatStat(t *testing.T, prep func(*exec.Cmd)) procStat {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "cat", "/proc/self/stat")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	prep(cmd)
	assert.NilError(t, cmd.Run())
	got := parseProcStat(t, buf.String())
	assert.Equal(t, got.pid, cmd.Process.Pid)
	return got
}

func selfProcStat(t *testing.T) procStat {
	t.Helper()
	b, err := os.ReadFile("/proc/self/stat")
	assert.NilError(t, err)
	return parseProcStat(t, string(b))
}

func TestPrepCommandAttrs_ChildLeadsItsOwnSession(t *testing.T) {
	self := selfProcStat(t)
	got := runCatStat(t, prepCommandAttrs)

	assert.Equal(t, got.session, got.pid)
	assert.Equal(t, got.pgrp, got.pid)
	assert.Assert(t, got.session != self.session,
		"child session %d must differ from the parent session %d", got.session, self.session)
}

func TestPrepHookAttrs_ChildStaysInParentSession(t *testing.T) {
	self := selfProcStat(t)
	got := runCatStat(t, prepHookAttrs)

	assert.Equal(t, got.session, self.session)
	assert.Equal(t, got.pgrp, got.pid)
}
