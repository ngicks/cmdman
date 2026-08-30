package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeReporterScript writes a shell script that reports a status about itself
// exactly once - guarded by a marker file, so a second run of the same command
// stays silent and a restart is observable - and then idles.
//
// The script names no command: identity comes from the CMDMAN_CMD_ID cmdman
// injects into everything it supervises. Its output goes to a log so a failure
// inside the supervised command is not mistaken for a status that never
// arrived.
func writeReporterScript(t *testing.T, dir string) (script, logPath string) {
	t.Helper()
	marker := filepath.Join(dir, "reported")
	logPath = filepath.Join(dir, "report.log")
	script = filepath.Join(dir, "report.sh")
	body := fmt.Sprintf(`#!/bin/sh
if [ ! -e %[1]q ]; then
	: > %[1]q
	%[2]q status set waiting --detail "needs input" > %[3]q 2>&1
	echo "exit=$?" >> %[3]q
fi
sleep 300
`, marker, cmdmanBin, logPath)
	must(t, os.WriteFile(script, []byte(body), 0o755))
	return script, logPath
}

// pollOutput runs args until its stdout equals want, failing with the last seen
// output when it never does.
func pollOutput(ctx context.Context, e *testEnv, want string, args ...string) {
	e.t.Helper()
	var last string
	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		last = e.Cmd(args...).Run(ctx, e.t)
		if last == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("cmdman %s: got %q, want %q", strings.Join(args, " "), last, want)
}

func TestStatus_ReportedBySupervisedCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dir := t.TempDir()
	script, logPath := writeReporterScript(t, dir)

	env.Cmd("run", "-n", "reporter", "--", "/bin/sh", script).Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "reporter") })
	env.waitForState(ctx, "reporter", "running", defaultTimeout)

	pollOutput(ctx, env, "waiting\tneeds input", "status", "get", "reporter")

	// The child's own view of the call, so a child-side failure cannot hide
	// behind an empty read from outside.
	report, err := os.ReadFile(logPath)
	must(t, err)
	if strings.TrimSpace(string(report)) != "exit=0" {
		t.Fatalf("supervised `status set` did not succeed; log:\n%s", report)
	}

	assertEq(
		t,
		env.Cmd("status", "get", "reporter", "--format", "{{.Status}}").Run(ctx, t),
		"waiting",
	)
	assertEq(
		t,
		env.Cmd("status", "get", "reporter", "--format", "{{.Detail}}").Run(ctx, t),
		"needs input",
	)

	env.Cmd("status", "delete", "reporter").Run(ctx, t)
	assertEq(t, env.Cmd("status", "get", "reporter").Run(ctx, t), "")

	// Set from outside, addressing the command by name, then restart: the report
	// must die with the run (D13). The script's marker keeps the second run
	// silent, so an empty read afterwards can only come from the restart.
	env.Cmd("status", "set", "working", "reporter", "--detail", "from outside").Run(ctx, t)
	assertEq(t, env.Cmd("status", "get", "reporter").Run(ctx, t), "working\tfrom outside")

	env.Cmd("restart", "reporter").Run(ctx, t)
	env.waitForState(ctx, "reporter", "running", defaultTimeout)
	pollOutput(ctx, env, "", "status", "get", "reporter")
}

func TestStatus_WithoutRunningMonitor(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Create(ctx, "idle", "/bin/sh", "-c", "sleep 30")

	// A read has an answer - nothing - even with no monitor to ask.
	assertEq(t, env.Cmd("status", "get", "idle").Run(ctx, t), "")

	// Writes do not: a report nobody holds is a report that was lost.
	for _, args := range [][]string{
		{"status", "set", "done", "idle"},
		{"status", "delete", "idle"},
	} {
		env.Cmd(args...).ExpectFail(ctx, t, "no running monitor")
	}

	// An unknown command is an error for every verb, reads included.
	for _, args := range [][]string{
		{"status", "get", "no-such-command"},
		{"status", "set", "done", "no-such-command"},
		{"status", "delete", "no-such-command"},
	} {
		env.Cmd(args...).ExpectFail(ctx, t, "resolve command")
	}

	// A status outside the vocabulary is rejected before anything is dialed.
	env.Cmd("status", "set", "busy", "idle").ExpectFail(ctx, t, "unknown status")

	// Neither an argument nor CMDMAN_CMD_ID: the verb says what is missing.
	env.Cmd("status", "get").ExpectFail(ctx, t, "CMDMAN_CMD_ID")
}

// TestStatus_AfterExit covers the other shape of "no monitor": a command whose
// run is over but whose state JSON still names the socket its monitor listened
// on. The dial then succeeds lazily and only the call fails, which is the path
// where a read must still answer "nothing" instead of erroring out.
func TestStatus_AfterExit(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.Cmd("run", "-n", "brief", "--", "/bin/sh", "-c", "exit 0").Run(ctx, t)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "brief") })
	env.waitForState(ctx, "brief", "exited", defaultTimeout)

	start := time.Now()
	res := env.Cmd("status", "get", "brief").Exec(ctx)
	elapsed := time.Since(start)
	if res.Err != nil {
		t.Fatalf("status get on an exited command failed: %v", res.Err)
	}
	assertEq(t, res.Stdout, "")
	// The read is bounded by the RPC deadline, not by the caller's patience.
	if elapsed > 10*time.Second {
		t.Errorf("status get on an exited command took %s", elapsed)
	}

	// A write still fails, with the same explanation an unstarted command gets:
	// how the monitor came to be absent is not the user's problem.
	env.Cmd("status", "set", "done", "brief").ExpectFail(ctx, t, "no running monitor")
}

func TestStatus_ComposeProjectView(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "ts-status"

	dir := t.TempDir()
	script, _ := writeReporterScript(t, dir)
	writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  reporter:
    args: [/bin/sh, %s]
  quiet:
    args: [/bin/sh, -c, "sleep 300"]
`, project, script))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	composeArgs := func(rest ...string) []string {
		return slices.Concat([]string{"compose", "--workdir", wd, "-f", composePath}, rest)
	}
	env.Cmd(composeArgs("up")...).Run(ctx, t)

	pollOutput(ctx, env, "quiet=\nreporter=waiting",
		composeArgs("status", "--format", "{{.Command}}={{.Status}}")...)

	// The default table carries the detail beside the status.
	table := env.Cmd(composeArgs("status")...).Run(ctx, t)
	lines := strings.Split(table, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected a header and two rows, got:\n%s", table)
	}
	if !strings.HasPrefix(lines[0], "COMMAND") || !strings.Contains(lines[0], "DETAIL") {
		t.Errorf("unexpected header %q", lines[0])
	}
	if !strings.Contains(lines[2], "waiting") || !strings.Contains(lines[2], "needs input") {
		t.Errorf("expected the reporter row to carry status and detail, got %q", lines[2])
	}

	// Command selection works exactly as it does for `compose ps`.
	assertEq(
		t,
		env.Cmd(composeArgs("status", "quiet", "--format", "{{.Command}}")...).Run(ctx, t),
		"quiet",
	)
	env.Cmd(composeArgs("status", "nope")...).ExpectFail(ctx, t, "unknown compose command")
}

func assertEq(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
