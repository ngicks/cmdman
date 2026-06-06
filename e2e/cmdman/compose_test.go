package cmdman_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// progressEvent mirrors one JSONL object emitted by compose up/start/stop/down
// on a non-terminal stdout. Each object reports a command's state transition and
// (on a terminal phase) its result.
type progressEvent struct {
	Op       string `json:"op"`
	Command  string `json:"command"`
	Phase    string `json:"phase"`
	Terminal bool   `json:"terminal"`
	ExitCode *int   `json:"exitCode"`
	Error    string `json:"error"`
}

// parseProgress parses the JSONL state trace from a compose lifecycle command's
// stdout. Non-JSON lines are skipped so the helper tolerates incidental output.
func parseProgress(t *testing.T, stdout string) []progressEvent {
	t.Helper()
	var events []progressEvent
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev progressEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse progress line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// progressReached reports whether command transitioned into phase in the trace.
func progressReached(events []progressEvent, command, phase string) bool {
	for _, ev := range events {
		if ev.Command == command && ev.Phase == phase {
			return true
		}
	}
	return false
}

// composeBasicYAML returns a minimal compose file with two commands that
// finish quickly (exit 0) so the test does not need to manage long-lived
// processes.
func composeBasicYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo alpha"]
  beta:
    args: [sh, -c, "echo beta"]
`, name)
}

// writeComposeFile writes content to <workdir>/cmd-compose.yaml.
func writeComposeFile(t *testing.T, workdir, content string) string {
	t.Helper()
	path := filepath.Join(workdir, "cmd-compose.yaml")
	must(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// composeWorkdir returns a fresh temp directory for a compose-test work directory.
func composeWorkdir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cmdman-e2e-compose-*")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// composeCommandLabel returns the cmdman.compose.command label embedded in
// the JSON form of a `cmdman ls --format '{{json .}}'` entry.
func composeCommandLabel(entry map[string]any) string {
	cfg, _ := entry["ConfigJSON"].(map[string]any)
	labels, _ := cfg["labels"].(map[string]any)
	v, _ := labels["cmdman.compose.command"].(string)
	return v
}

// cleanupProject removes every command that carries the given project label.
func cleanupProject(ctx context.Context, e *testEnv, workdir, project string) {
	entries, _, _ := e.exec(ctx, "ls", "-a",
		"-l", "cmdman.compose.workdir="+workdir,
		"-l", "cmdman.compose.project="+project,
		"--format", "{{.ID}}",
	)
	for id := range strings.FieldsSeq(entries) {
		e.exec(ctx, "rm", "-f", id)
	}
}

func TestComposeCreate(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-create"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd,
		"-f", filepath.Join(wd, "cmd-compose.yaml"), "create")
	if err != nil {
		t.Fatalf("compose create failed: %v\nstdout:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "create       alpha") ||
		!strings.Contains(stdout, "create       beta") {
		t.Fatalf("expected create lines for alpha and beta; got:\n%s", stdout)
	}

	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 2 {
		t.Fatalf("expected 2 project-labeled commands, got %d", len(entries))
	}
	for _, e := range entries {
		name, _ := e["Name"].(string)
		// Generated name format: <12-hex-workdir-hash>-<escaped-project>-<escaped-command>.
		// Project "tc-create" escapes to "tc--create"; commands "alpha"/"beta" are unchanged.
		if !strings.Contains(name, "-tc--create-alpha") &&
			!strings.Contains(name, "-tc--create-beta") {
			t.Fatalf("generated name %q does not look like a compose generated name", name)
		}
	}
}

func TestComposeLsAndPs(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-ls-ps"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
		}
	}

	lsOut, lsErr, err := env.exec(ctx, "compose", "ls")
	if err != nil {
		t.Fatalf("compose ls failed: %v\nstdout:\n%s\nstderr:\n%s", err, lsOut, lsErr)
	}
	if !strings.Contains(lsOut, "PROJECT") ||
		!strings.Contains(lsOut, "COMMANDS") ||
		!strings.Contains(lsOut, project) ||
		!strings.Contains(lsOut, wd) {
		t.Fatalf("expected compose project in ls output; got:\n%s", lsOut)
	}

	psOut, psErr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "ps")
	if err != nil {
		t.Fatalf("compose ps failed: %v\nstdout:\n%s\nstderr:\n%s", err, psOut, psErr)
	}
	if !strings.Contains(psOut, "COMMAND") ||
		!strings.Contains(psOut, "ID") ||
		!strings.Contains(psOut, "alpha") ||
		!strings.Contains(psOut, "beta") ||
		!strings.Contains(psOut, "exited") {
		t.Fatalf("expected compose commands in ps output; got:\n%s", psOut)
	}
}

func TestComposeUpIdempotent(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-up-idem"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd,
		"-f", filepath.Join(wd, "cmd-compose.yaml"), "up")
	if err != nil {
		t.Fatalf("compose up #1 failed: %v\nstdout:\n%s", err, stdout)
	}
	if events := parseProgress(t, stdout); !progressReached(events, "alpha", "created") {
		t.Fatalf("first up should create alpha; got:\n%s", stdout)
	}

	idsBefore := map[string]string{}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		idsBefore[e["Name"].(string)] = e["ID"].(string)
	}

	stdout2, _, err := env.exec(ctx, "compose", "--workdir", wd,
		"-f", filepath.Join(wd, "cmd-compose.yaml"), "up")
	if err != nil {
		t.Fatalf("compose up #2 failed: %v\nstdout:\n%s", err, stdout2)
	}
	events2 := parseProgress(t, stdout2)
	if !progressReached(events2, "alpha", "unchanged") ||
		!progressReached(events2, "beta", "unchanged") {
		t.Fatalf("second up should report unchanged for both; got:\n%s", stdout2)
	}

	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		name := e["Name"].(string)
		if idsBefore[name] != e["ID"].(string) {
			t.Fatalf("command %q id changed across idempotent up: was %s, now %s",
				name, idsBefore[name], e["id"])
		}
	}
}

func TestComposeUpRecreateOnArgsChange(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-recreate"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up #1 failed: %v", err)
	}

	// Wait for the short-running commands to exit so recreate isn't skipped.
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
		}
	}

	idsBefore := map[string]string{}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		idsBefore[composeCommandLabel(e)] = e["ID"].(string)
	}

	// Change alpha only; beta is unchanged.
	changed := strings.Replace(composeBasicYAML(project),
		`"echo alpha"`, `"echo alpha-v2"`, 1)
	must(t, os.WriteFile(composePath, []byte(changed), 0o644))

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	if err != nil {
		t.Fatalf("compose up #2 failed: %v\nstdout:\n%s", err, stdout)
	}
	events := parseProgress(t, stdout)
	if !progressReached(events, "alpha", "recreated") {
		t.Fatalf("expected recreate for alpha; got:\n%s", stdout)
	}
	if !progressReached(events, "beta", "unchanged") {
		t.Fatalf("expected beta unchanged; got:\n%s", stdout)
	}

	idsAfter := map[string]string{}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		idsAfter[composeCommandLabel(e)] = e["ID"].(string)
	}

	if idsBefore["alpha"] == idsAfter["alpha"] {
		t.Fatalf("alpha id should have changed: %s", idsAfter["alpha"])
	}
	if idsBefore["beta"] != idsAfter["beta"] {
		t.Fatalf("beta id should be stable: was %s, now %s", idsBefore["beta"], idsAfter["beta"])
	}
}

// TestComposeUpRecreateRunningCommand verifies that `compose up` recreates a
// command whose config changed even while it is still running: the live instance
// is stopped first (surfaced as stopping → stopped in the progress trace), then
// removed, recreated, and started again under a fresh ID.
func TestComposeUpRecreateRunningCommand(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-recreate-running"
	composePath := writeComposeFile(t, wd, composeRecreateRunningYAML(project, "30"))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up #1 failed: %v", err)
	}

	// Wait until alpha is actually running so the recreate is forced to stop it.
	alphaID := func() string {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command=alpha",
			"-l", "cmdman.compose.project="+project,
		) {
			return e["ID"].(string)
		}
		return ""
	}
	idBefore := alphaID()
	if idBefore == "" {
		t.Fatalf("alpha was not created by up #1")
	}
	env.waitForState(ctx, idBefore, "running", 5*time.Second)

	// Change alpha's args so its config hash differs, forcing a recreate while
	// the command is still running.
	must(t, os.WriteFile(composePath, []byte(composeRecreateRunningYAML(project, "31")), 0o644))

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	if err != nil {
		t.Fatalf("compose up #2 failed: %v\nstdout:\n%s", err, stdout)
	}

	// The progress trace must show the running instance going down (stopping →
	// stopped) before it is recreated and started again.
	events := parseProgress(t, stdout)
	for _, phase := range []string{"stopping", "stopped", "recreated", "running"} {
		if !progressReached(events, "alpha", phase) {
			t.Fatalf("expected alpha to reach %q during recreate; got:\n%s", phase, stdout)
		}
	}

	idAfter := alphaID()
	if idAfter == "" {
		t.Fatalf("alpha missing after recreate")
	}
	if idAfter == idBefore {
		t.Fatalf("alpha id should have changed after recreate: still %s", idAfter)
	}
	env.waitForState(ctx, idAfter, "running", 5*time.Second)
}

// composeRecreateRunningYAML returns a one-command compose file whose single
// long-running command sleeps for the given duration. Varying the duration
// changes the config hash, which is enough to force a recreate.
func composeRecreateRunningYAML(name, sleep string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, %q]
`, name, sleep)
}

// composeSingleYAML returns a minimal compose file with one command (alpha only).
func composeSingleYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo alpha"]
`, name)
}

// composeLongRunningYAML returns a compose file where command A runs sleep 30.
func composeLongRunningYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sleep, "30"]
`, name)
}

// TestComposeOrphanWarnByDefault creates a project with two commands, rewrites
// the compose file to keep only one, and verifies that compose create (without
// --remove-orphan) warns about the orphan on stderr while leaving both commands
// in place.
func TestComposeOrphanWarnByDefault(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-orphan-warn"
	composePath := filepath.Join(wd, "cmd-compose.yaml")
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Initial create: both alpha and beta.
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("initial compose create failed: %v", err)
	}

	// Rewrite to alpha only; beta becomes an orphan.
	must(t, os.WriteFile(composePath, []byte(composeSingleYAML(project)), 0o644))

	// Enable warn-level logging so slog.Warn output reaches stderr.
	stdout, stderr, err := env.execWithExtraEnv(ctx,
		[]string{"CMDMAN_LOG_LEVEL=warn"},
		"compose", "--workdir", wd, "-f", composePath, "create",
	)
	if err != nil {
		t.Fatalf(
			"compose create (orphan warn) failed: %v\nstdout:\n%s\nstderr:\n%s",
			err,
			stdout,
			stderr,
		)
	}

	// Both commands must still exist.
	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 2 {
		t.Fatalf(
			"expected both commands to remain (got %d); stdout:\n%s\nstderr:\n%s",
			len(entries),
			stdout,
			stderr,
		)
	}

	// Orphan warning must appear on stderr (slog default writer, enabled via
	// CMDMAN_LOG_LEVEL=warn).
	if !strings.Contains(stderr, "orphan") {
		t.Fatalf("expected orphan warning on stderr; got:\n%s", stderr)
	}
}

// TestComposeOrphanRemoved creates a project with two commands, rewrites the
// compose file to keep only alpha, and verifies that compose create
// --remove-orphan removes beta and prints a remove-orphan line.
func TestComposeOrphanRemoved(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-orphan-rm"
	composePath := filepath.Join(wd, "cmd-compose.yaml")
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Initial create: both alpha and beta.
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("initial compose create failed: %v", err)
	}

	// Rewrite to alpha only; beta becomes an orphan.
	must(t, os.WriteFile(composePath, []byte(composeSingleYAML(project)), 0o644))

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"create", "--remove-orphan")
	if err != nil {
		t.Fatalf("compose create --remove-orphan failed: %v\nstdout:\n%s", err, stdout)
	}

	// Only alpha should remain.
	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 1 {
		t.Fatalf("expected only alpha to remain (got %d); stdout:\n%s", len(entries), stdout)
	}
	if composeCommandLabel(entries[0]) != "alpha" {
		t.Fatalf("remaining command should be alpha; got label %q", composeCommandLabel(entries[0]))
	}

	// Summary must include a remove-orphan line for beta.
	if !strings.Contains(stdout, "remove-orphan") || !strings.Contains(stdout, "beta") {
		t.Fatalf("expected remove-orphan beta in summary; got:\n%s", stdout)
	}
}

// TestComposeRunningOrphanSkipped creates a project with a long-running command
// (sleep 30), rewrites the compose file to remove it, and verifies that
// compose create --remove-orphan leaves the running command in place and
// reports a "skipped" indicator.
func TestComposeRunningOrphanSkipped(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-orphan-skip"
	composePath := filepath.Join(wd, "cmd-compose.yaml")
	writeComposeFile(t, wd, composeLongRunningYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// Create + start alpha (sleep 30) so it ends up running.
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	// Wait until alpha is running.
	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 1 {
		t.Fatalf("expected 1 command after up, got %d", len(entries))
	}
	alphaID := entries[0]["ID"].(string)
	env.waitForState(ctx, alphaID, "running", 10*time.Second)

	// Clean up alpha on test exit.
	t.Cleanup(func() {
		env.exec(ctx, "stop", alphaID)
		time.Sleep(300 * time.Millisecond)
		env.exec(ctx, "rm", "-f", alphaID)
	})

	// Rewrite compose file to be empty of commands — alpha becomes an orphan.
	emptyYAML := fmt.Sprintf("name: %s\ncommands: {}\n", project)
	must(t, os.WriteFile(composePath, []byte(emptyYAML), 0o644))

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"create", "--remove-orphan")
	// The command exits with error because the skipped orphan is reported as an action error.
	_ = err

	// Alpha must still exist (it is running and was skipped).
	remaining := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(remaining) != 1 {
		t.Fatalf("expected running alpha to remain (got %d); stdout:\n%s\nstderr:\n%s",
			len(remaining), stdout, stderr)
	}

	// A "skipped" line must appear in stdout (summary) or a warning in stderr.
	hasSkippedInSummary := strings.Contains(stdout, "skipped") || strings.Contains(stdout, "error")
	hasSkippedInLog := strings.Contains(stderr, "skipping removal") ||
		strings.Contains(stderr, "running")
	if !hasSkippedInSummary && !hasSkippedInLog {
		t.Fatalf(
			"expected skipped indicator in stdout or stderr;\nstdout:\n%s\nstderr:\n%s",
			stdout,
			stderr,
		)
	}
}

// composeAfterYAML produces a YAML with two long-running commands where
// `worker` declares `after: api` with the `running` condition (so up doesn't
// wait for api to terminate before starting worker).
func composeAfterYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  api:
    args: [sleep, "30"]
  worker:
    args: [sleep, "30"]
    after:
      api:
        condition: running
`, name)
}

func TestComposeStop(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop"
	writeComposeFile(t, wd, composeLongRunningYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	// Wait until alpha is running before stopping.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "stop")
	if err != nil {
		t.Fatalf("compose stop failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if events := parseProgress(t, stdout); !progressReached(events, "alpha", "stopped") {
		t.Fatalf("expected alpha to reach stopped in progress trace; got:\n%s", stdout)
	}

	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		// "stopped" maps to either exited or failed depending on signal handling.
		st := e["State"].(string)
		if st != "exited" && st != "failed" {
			t.Fatalf("expected exited/failed after stop, got %q", st)
		}
	}
}

func TestComposeStopProjectOnly(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop-po"
	writeComposeFile(t, wd, composeLongRunningYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	// Second invocation: project + workdir only, no -f.
	stdout, stderr, err := env.exec(ctx, "compose",
		"--workdir", wd, "--project-name", project, "stop")
	if err != nil {
		t.Fatalf("compose stop (project-only) failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr)
	}
	events := parseProgress(t, stdout)
	stoppedAny := false
	for _, ev := range events {
		if ev.Phase == "stopped" {
			stoppedAny = true
			break
		}
	}
	if !stoppedAny {
		t.Fatalf("expected a stopped event in the project-only stop trace; got:\n%s", stdout)
	}
}

func TestComposeDown(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-down"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "down")
	if err != nil {
		t.Fatalf("compose down failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 0 {
		t.Fatalf("expected 0 commands after down, got %d", len(entries))
	}
}

// TestComposeProgressJSONL verifies the JSONL state trace emitted on a
// non-terminal stdout: up reports created→running per command (with the op and
// terminal flags set), --progress=quiet suppresses output, and down reports
// removed events.
func TestComposeProgressJSONL(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-progress"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")

	// up: auto mode on a piped (non-terminal) stdout resolves to JSONL.
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	if err != nil {
		t.Fatalf("compose up failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	events := parseProgress(t, stdout)
	if len(events) == 0 {
		t.Fatalf("expected JSONL progress events on non-terminal stdout; got:\n%s", stdout)
	}
	for _, ev := range events {
		if ev.Op != "up" {
			t.Fatalf("expected op=up on every event, got %q in:\n%s", ev.Op, stdout)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		if !progressReached(events, name, "created") {
			t.Fatalf("expected %q created event; got:\n%s", name, stdout)
		}
		if !progressReached(events, name, "running") {
			t.Fatalf("expected %q running event; got:\n%s", name, stdout)
		}
	}
	// Terminal phases carry the terminal flag; transient phases do not.
	for _, ev := range events {
		switch ev.Phase {
		case "running", "created", "exited", "removed", "stopped", "unchanged":
			if !ev.Terminal {
				t.Fatalf("phase %q should be terminal; got:\n%s", ev.Phase, stdout)
			}
		case "starting", "creating", "stopping", "removing", "waiting":
			if ev.Terminal {
				t.Fatalf("phase %q should not be terminal; got:\n%s", ev.Phase, stdout)
			}
		}
	}

	// quiet: no progress output at all.
	qOut, qErr, err := env.exec(ctx, "compose",
		"--workdir", wd, "-f", composePath, "up", "--progress", "quiet")
	if err != nil {
		t.Fatalf("compose up --progress quiet failed: %v\nstderr:\n%s", err, qErr)
	}
	if strings.TrimSpace(qOut) != "" {
		t.Fatalf("expected no stdout with --progress quiet; got:\n%s", qOut)
	}

	// down with an explicit --progress json forces JSONL and reports removals.
	dOut, dErr, err := env.exec(ctx, "compose",
		"--workdir", wd, "-f", composePath, "down", "--progress", "json")
	if err != nil {
		t.Fatalf("compose down failed: %v\nstdout:\n%s\nstderr:\n%s", err, dOut, dErr)
	}
	downEvents := parseProgress(t, dOut)
	for _, name := range []string{"alpha", "beta"} {
		if !progressReached(downEvents, name, "removed") {
			t.Fatalf("expected %q removed event on down; got:\n%s", name, dOut)
		}
	}
	for _, ev := range downEvents {
		if ev.Op != "down" {
			t.Fatalf("expected op=down on every down event, got %q in:\n%s", ev.Op, dOut)
		}
	}
}

// TestComposeDownByCwdTargetsAllProjectsInWorkdir verifies that, without -f or
// --project-name, down resolves the project by working directory and therefore
// tears down every project sharing that workdir.
func TestComposeDownByCwdTargetsAllProjectsInWorkdir(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	// Keep the spec out of wd so wd holds no discoverable compose file; down
	// must then resolve purely by --workdir.
	specDir := composeWorkdir(t)
	composePath := writeComposeFile(t, specDir, composeBasicYAML("ignored"))
	projA, projB := "tc-cwd-a", "tc-cwd-b"
	t.Cleanup(func() {
		cleanupProject(ctx, env, wd, projA)
		cleanupProject(ctx, env, wd, projB)
	})

	for _, p := range []string{projA, projB} {
		if _, stderr, err := env.exec(ctx, "compose", "--workdir", wd,
			"-f", composePath, "--project-name", p, "create"); err != nil {
			t.Fatalf("compose create %s failed: %v\nstderr:\n%s", p, err, stderr)
		}
	}
	if got := len(env.lsJSON(ctx, "-l", "cmdman.compose.workdir="+wd)); got != 4 {
		t.Fatalf("expected 4 commands across both projects, got %d", got)
	}

	// No -f and no --project-name: resolved by cwd (workdir), so it targets every
	// command in wd regardless of project.
	if _, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "down"); err != nil {
		t.Fatalf("compose down (cwd) failed: %v\nstderr:\n%s", err, stderr)
	}
	if got := len(env.lsJSON(ctx, "-l", "cmdman.compose.workdir="+wd)); got != 0 {
		t.Fatalf("expected 0 commands after cwd-based down, got %d", got)
	}
}

func TestComposeDownRemovesOrphans(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-down-orph"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	// Rewrite YAML to drop beta; beta is now an orphan.
	writeComposeFile(t, wd, composeSingleYAML(project))

	// Project-only down (no -f) should still tear down the entire (workdir, project) pair.
	if _, _, err := env.exec(ctx, "compose",
		"--workdir", wd, "--project-name", project, "down"); err != nil {
		t.Fatalf("compose down failed: %v", err)
	}

	entries := env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	)
	if len(entries) != 0 {
		t.Fatalf("expected 0 commands after down (including orphans), got %d", len(entries))
	}
}

func TestComposeRestart(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-restart"
	writeComposeFile(t, wd, composeLongRunningYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "restart")
	if err != nil {
		t.Fatalf("compose restart failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// Restart should produce a line per command.
	if !strings.Contains(stdout, "alpha") {
		t.Fatalf("expected alpha line in restart output; got:\n%s", stdout)
	}

	// After restart, alpha should be running again.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}
}

func TestComposeReverseDepOrderStop(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-revdep"
	writeComposeFile(t, wd, composeAfterYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	// Wait for both commands to reach running.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	// Stop should walk the DAG in reverse: worker before api.
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "stop")
	if err != nil {
		t.Fatalf("compose stop failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	idxWorker := strings.Index(stdout, "worker")
	idxAPI := strings.Index(stdout, "api")
	if idxWorker < 0 || idxAPI < 0 {
		t.Fatalf("expected both api and worker in stop output; got:\n%s", stdout)
	}
	if idxWorker > idxAPI {
		t.Fatalf(
			"expected worker (dependent) before api (dep) in reverse-dep stop output; got:\n%s",
			stdout,
		)
	}
}

// TestComposeStopByNameStopsDependents verifies that stopping only the named
// dependency also stops its recursive dependents (named + dependents closure),
// so a command is never left running with a dead dependency.
func TestComposeStopByNameStopsDependents(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-stop-deps"
	writeComposeFile(t, wd, composeAfterYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	// Stop only the dependency by name; its dependent must be torn down too.
	stdout, stderr, err := env.exec(ctx, "compose",
		"--workdir", wd, "-f", composePath, "stop", "api")
	if err != nil {
		t.Fatalf("compose stop api failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	idxWorker := strings.Index(stdout, "worker")
	idxAPI := strings.Index(stdout, "api")
	if idxWorker < 0 || idxAPI < 0 {
		t.Fatalf("expected both worker and api in stop output; got:\n%s", stdout)
	}
	if idxWorker > idxAPI {
		t.Fatalf("expected worker (dependent) stopped before api; got:\n%s", stdout)
	}

	// Both commands must now be terminal.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		st := e["State"].(string)
		if st != "exited" && st != "failed" {
			t.Fatalf("expected api and worker exited/failed after stopping api, got %q", st)
		}
	}
}

// composeLogYAML produces a YAML where each command prints one line and exits.
func composeLogYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo line-from-alpha"]
  beta:
    args: [sh, -c, "echo line-from-beta"]
`, name)
}

// composeFollowLogYAML defines two commands that each emit a stored line, go
// quiet, then emit a live line and idle. The quiet window after the stored line
// is the case that exercises the stock/live split: the follow starts while both
// commands are silent, so the live phase must not re-emit the stored lines.
func composeFollowLogYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo alpha-stock; sleep 1; echo alpha-live; sleep 60"]
  beta:
    args: [sh, -c, "echo beta-stock; sleep 1; echo beta-live; sleep 60"]
`, name)
}

func TestComposeSignal(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-signal"
	writeComposeFile(t, wd, composeLongRunningYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"signal", "--signal", "SIGTERM")
	if err != nil {
		t.Fatalf("compose signal failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "signaled     alpha") {
		t.Fatalf("expected signaled line; got:\n%s", stdout)
	}

	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		// Wait for the SIGTERM to take effect.
		env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
	}
}

func TestComposeSignalRequired(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-sig-req"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "signal")
	if err == nil {
		t.Fatalf("compose signal without --signal should fail; stdout=%q stderr=%q",
			stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "signal") || !strings.Contains(combined, "required") {
		t.Fatalf("expected stderr to mention --signal required; got:\n%s", combined)
	}
}

func TestComposeWait(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-wait"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "wait")
	if err != nil {
		t.Fatalf("compose wait failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "alpha") || !strings.Contains(stdout, "beta") {
		t.Fatalf("expected both commands in wait output; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "exit code: 0") {
		t.Fatalf("expected exit code 0 in wait output; got:\n%s", stdout)
	}
}

func TestComposeLogsMerged(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-logs"
	writeComposeFile(t, wd, composeLogYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	// Wait for both commands to exit so logs are present.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "logs")
	if err != nil {
		t.Fatalf("compose logs failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, " alpha |line-from-alpha") ||
		!strings.Contains(stdout, " beta |line-from-beta") {
		t.Fatalf("expected timestamped per-command prefixes in merged log output; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "line-from-alpha") ||
		!strings.Contains(stdout, "line-from-beta") {
		t.Fatalf("expected both commands' log lines; got:\n%s", stdout)
	}
}

// TestComposeLogsFollowStockThenLive checks that `compose logs --follow` emits
// the merged stored logs once and then tails live output. It guards the
// regression where the storage/subscription bridge re-read stored logs from
// byte zero when the live phase's Since filter excluded every stored record
// (the commands are quiet when the follow begins), duplicating the stock.
func TestComposeLogsFollowStockThenLive(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-logs-follow"
	writeComposeFile(t, wd, composeFollowLogYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
	}
	// Both stored lines must be persisted before following, so the follow starts
	// while the commands are quiet.
	waitUntil(t, 5*time.Second, func() bool {
		out, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "logs")
		return err == nil &&
			strings.Contains(out, "alpha-stock") &&
			strings.Contains(out, "beta-stock")
	}, "stored lines persisted for both commands")

	followCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	stdout, _, _ := env.exec(
		followCtx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"logs",
		"--follow",
	)

	for _, line := range []string{"alpha-stock", "beta-stock"} {
		if got := strings.Count(stdout, line); got != 1 {
			t.Fatalf(
				"expected stored line %q exactly once "+
					"(must not be duplicated by the live bridge), got %d:\n%s",
				line,
				got,
				stdout,
			)
		}
	}
	for _, line := range []string{"alpha-live", "beta-live"} {
		if !strings.Contains(stdout, line) {
			t.Fatalf("expected live line %q while following:\n%s", line, stdout)
		}
	}
}

// composeDependencyMarkerYAML produces a YAML where `api` writes a marker file
// then exits, and `worker` reads the marker after `api` completes. If the
// after-condition is honored, the worker will see the file. Otherwise it sees
// "missing".
func composeDependencyMarkerYAML(name, markerPath string) string {
	// Single-quote markerPath so spaces or metacharacters in TMPDIR don't
	// break the shell snippets. Single-quote any embedded apostrophes by
	// closing, escaping, and reopening: ' → '\''
	q := "'" + strings.ReplaceAll(markerPath, "'", `'\''`) + "'"
	return fmt.Sprintf(`name: %s
commands:
  api:
    args: [sh, -c, "sleep 1; echo api-done > %s"]
  worker:
    args: [sh, -c, "if [ -f %s ]; then cat %s; else echo missing; fi"]
    after:
      api:
        condition: completed
`, name, q, q, q)
}

func TestComposeUpDependencyOrdered(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-depord"
	markerPath := filepath.Join(wd, "marker.txt")
	writeComposeFile(t, wd, composeDependencyMarkerYAML(project, markerPath))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	if err != nil {
		t.Fatalf("compose up failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Wait for both to exit.
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
	) {
		env.waitForState(ctx, e["ID"].(string), "exited", 10*time.Second)
	}

	// Inspect worker's logs via cmdman logs.
	var workerID string
	for _, e := range env.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
		"-l", "cmdman.compose.command=worker",
	) {
		workerID = e["ID"].(string)
	}
	if workerID == "" {
		t.Fatalf("worker command not found")
	}
	logs, _, err := env.exec(ctx, "logs", workerID)
	if err != nil {
		t.Fatalf("cmdman logs failed: %v", err)
	}
	if !strings.Contains(logs, "api-done") {
		t.Fatalf("worker should observe api's marker file (after.completed); got logs:\n%s", logs)
	}
	if strings.Contains(logs, "missing") {
		t.Fatalf("worker started before api completed; got logs:\n%s", logs)
	}
}

func TestComposeStartSubcommand(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-startsub"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "start")
	if err != nil {
		t.Fatalf("compose start failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	events := parseProgress(t, stdout)
	if !progressReached(events, "alpha", "running") ||
		!progressReached(events, "beta", "running") {
		t.Fatalf("expected alpha and beta to reach running in progress trace; got:\n%s", stdout)
	}
}

func TestComposeStartFilterIncludesDependencies(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-startdep"
	writeComposeFile(t, wd, composeAfterYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	// Start only worker — api should be pulled in as a dependency.
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"start", "worker")
	if err != nil {
		t.Fatalf("compose start worker failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// composeAfterYAML uses default condition (completed) with sleep 30 — the
	// start call should not wait for completion in this synchronous test; both
	// commands will have been started (api triggers worker after termination,
	// but Start returns once both are running). With "completed" condition,
	// worker is gated on api's exit; for this fixture, we only need to assert
	// api got started (the dep expansion worked).
	if !strings.Contains(stdout, "api") {
		t.Fatalf("expected api in start output (pulled as dependency); got:\n%s", stdout)
	}
}

func TestComposeEmptyProjectTarget(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)

	// Project does not exist; --project-name + --workdir resolves to an empty target set.
	// CMDMAN_LOG_LEVEL=warn enables the structured-log event the test asserts on.
	stdout, stderr, err := env.execWithExtraEnv(ctx,
		[]string{"CMDMAN_LOG_LEVEL=warn"},
		"compose", "--workdir", wd, "--project-name", "tc-empty", "stop")
	if err != nil {
		t.Fatalf(
			"compose stop on empty project should exit 0; got err=%v\nstdout:\n%s\nstderr:\n%s",
			err,
			stdout,
			stderr,
		)
	}
	// A structured log event should appear in stderr describing the empty target.
	if !strings.Contains(stderr, "tc-empty") &&
		!strings.Contains(stderr, "no commands") &&
		!strings.Contains(stderr, "empty") {
		t.Fatalf("expected empty-project log on stderr; got stderr:\n%s", stderr)
	}
}

// ---- reconciliation: re-start, dependency conditions, concurrency -----------

// composeCounterYAML produces a YAML whose single command appends one line to
// counterPath each time it runs, so re-runs are observable by counting lines.
func composeCounterYAML(name, counterPath string) string {
	q := shellQuote(counterPath)
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo run >> %s"]
`, name, q)
}

// composeMissingBinYAML points the single command at scriptPath as argv[0]. When
// scriptPath does not exist, the monitor sets state=failed; once materialised,
// the command can be started again.
func composeMissingBinYAML(name, scriptPath string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [%s]
`, name, shellQuote(scriptPath))
}

// composeTwoSlowYAML produces two independent long-running commands that each
// write a marker file on start, then sleep, so concurrent start-up is observable
// via marker files rather than timing.
func composeTwoSlowYAML(name, aMarker, bMarker string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    args: [sh, -c, "echo started > %s; sleep 30"]
  beta:
    args: [sh, -c, "echo started > %s; sleep 30"]
`, name, shellQuote(aMarker), shellQuote(bMarker))
}

// shellQuote single-quotes a path for embedding in a double-quoted YAML scalar.
func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// countNonEmptyLines counts non-blank lines in s.
func countNonEmptyLines(s string) int {
	n := 0
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readFile returns the file contents, or "" when the file does not exist yet so
// callers can poll for it.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// composeCommandID returns the cmdman ID of a single compose command, or "".
func composeCommandID(ctx context.Context, e *testEnv, wd, project, command string) string {
	for _, entry := range e.lsJSON(ctx,
		"-l", "cmdman.compose.workdir="+wd,
		"-l", "cmdman.compose.project="+project,
		"-l", "cmdman.compose.command="+command,
	) {
		if id, ok := entry["ID"].(string); ok {
			return id
		}
	}
	return ""
}

// TestComposeUpRestartsExitedCommand verifies that a second `compose up` starts
// a command that already exited, increasing its run count.
func TestComposeUpRestartsExitedCommand(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-up-restart"
	counter := filepath.Join(wd, "runs.txt")
	writeComposeFile(t, wd, composeCounterYAML(project, counter))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")

	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("first compose up failed: %v", err)
	}
	alphaID := composeCommandID(ctx, env, wd, project, "alpha")
	if alphaID == "" {
		t.Fatal("alpha command not found after first up")
	}
	env.waitForState(ctx, alphaID, "exited", 10*time.Second)
	if got := countNonEmptyLines(readFile(t, counter)); got != 1 {
		t.Fatalf("expected 1 run after first up, got %d", got)
	}

	// alpha is exited; a second up must start it again.
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("second compose up failed: %v", err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		return countNonEmptyLines(readFile(t, counter)) >= 2
	}, "alpha re-ran on the second up")
}

// TestComposeStartFromFailedState verifies `compose start` re-starts a command
// left in the failed state once its executable is fixed, mirroring cmdman start.
func TestComposeStartFromFailedState(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-start-failed"
	scriptPath := filepath.Join(wd, "later.sh") // does not exist yet
	writeComposeFile(t, wd, composeMissingBinYAML(project, scriptPath))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}
	alphaID := composeCommandID(ctx, env, wd, project, "alpha")
	if alphaID == "" {
		t.Fatal("alpha command not found after create")
	}

	// First start must fail: argv[0] does not exist → failed state.
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"start",
	); err == nil {
		t.Fatal("expected compose start to fail while the binary is missing")
	}
	env.waitForState(ctx, alphaID, "failed", 10*time.Second)

	// Materialise the binary; start from failed must now succeed.
	must(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "start")
	if err != nil {
		t.Fatalf("compose start from failed should succeed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr)
	}
	if events := parseProgress(t, stdout); !progressReached(events, "alpha", "running") {
		t.Fatalf("expected alpha to reach running in progress trace; got:\n%s", stdout)
	}
	env.waitForState(ctx, alphaID, "exited", 10*time.Second)
	if code, _ := env.inspectJSON(ctx, alphaID)["exit_code"].(float64); code != 0 {
		t.Fatalf("expected exit_code=0 after restart from failed, got %v", code)
	}
}

// TestComposeUpRunningConditionDoesNotWaitForExit verifies a `running` condition
// releases the dependent promptly while the long-running dependency keeps
// running, rather than blocking on its termination.
func TestComposeUpRunningConditionDoesNotWaitForExit(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-up-running"
	writeComposeFile(
		t,
		wd,
		composeAfterYAML(project),
	) // api+worker sleep 30, worker after api started
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	start := time.Now()
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("compose up failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if elapsed > 20*time.Second {
		t.Fatalf(
			"up blocked on the long-running dependency (%v); running condition must not wait",
			elapsed,
		)
	}

	// Both commands should be running concurrently; neither has exited.
	for _, name := range []string{"api", "worker"} {
		id := composeCommandID(ctx, env, wd, project, name)
		if id == "" {
			t.Fatalf("%s command not found", name)
		}
		env.waitForState(ctx, id, "running", 10*time.Second)
	}
}

// TestComposeUpReRunHonorsCompletedDependency is the e2e form of the core fix: a
// `completed` dependent must wait for the dependency's NEW run, not proceed from
// its stale terminal state. The marker is deleted between runs; a buggy
// implementation would let worker observe "missing".
func TestComposeUpReRunHonorsCompletedDependency(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-up-rerun-completed"
	markerPath := filepath.Join(wd, "marker.txt")
	writeComposeFile(t, wd, composeDependencyMarkerYAML(project, markerPath))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	workerLogs := func() string {
		id := composeCommandID(ctx, env, wd, project, "worker")
		if id == "" {
			return ""
		}
		out, _, _ := env.exec(ctx, "logs", id)
		return out
	}

	// First run: api writes the marker, worker observes it after api completes.
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("first compose up failed: %v", err)
	}
	waitUntil(t, 15*time.Second, func() bool {
		return countNonEmptyLines(workerLogs()) >= 1
	}, "worker ran once")

	// Remove the marker and re-run. With the fix, up waits for api's new
	// completion (which re-creates the marker) before starting worker.
	must(t, os.Remove(markerPath))
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("second compose up failed: %v", err)
	}
	waitUntil(t, 15*time.Second, func() bool {
		return countNonEmptyLines(workerLogs()) >= 2
	}, "worker ran a second time")

	if strings.Contains(workerLogs(), "missing") {
		t.Fatalf("worker proceeded from api's stale terminal state; got logs:\n%s", workerLogs())
	}
}

// TestComposeUpIndependentCommandsStartConcurrently verifies two independent
// slow commands both begin before either could finish, asserted via marker
// files.
func TestComposeUpIndependentCommandsStartConcurrently(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-up-concurrent"
	aMarker := filepath.Join(wd, "alpha.started")
	bMarker := filepath.Join(wd, "beta.started")
	writeComposeFile(t, wd, composeTwoSlowYAML(project, aMarker, bMarker))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}

	// Both commands sleep 30s, so both markers can only coexist if both began.
	waitUntil(t, 15*time.Second, func() bool {
		return fileExists(aMarker) && fileExists(bMarker)
	}, "both independent commands started")

	for _, name := range []string{"alpha", "beta"} {
		id := composeCommandID(ctx, env, wd, project, name)
		env.waitForState(ctx, id, "running", 10*time.Second)
	}
}
