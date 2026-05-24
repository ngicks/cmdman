package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	if !strings.Contains(lsOut, "PROJECT\tCOMMANDS") ||
		!strings.Contains(lsOut, project) ||
		!strings.Contains(lsOut, wd) {
		t.Fatalf("expected compose project in ls output; got:\n%s", lsOut)
	}

	psOut, psErr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "ps")
	if err != nil {
		t.Fatalf("compose ps failed: %v\nstdout:\n%s\nstderr:\n%s", err, psOut, psErr)
	}
	if !strings.Contains(psOut, "COMMAND\tID") ||
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
	if !strings.Contains(stdout, "create       alpha") {
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
	if !strings.Contains(stdout2, "unchanged    alpha") ||
		!strings.Contains(stdout2, "unchanged    beta") {
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
	if !strings.Contains(stdout, "recreate     alpha") {
		t.Fatalf("expected recreate for alpha; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "unchanged    beta") {
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
// `worker` declares `after: api` with the `started` condition (so up doesn't
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
        condition: started
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
	if !strings.Contains(stdout, "stopped      alpha") {
		t.Fatalf("expected stopped line for alpha; got:\n%s", stdout)
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
	if !strings.Contains(stdout, "stopped") {
		t.Fatalf("expected stopped line; got:\n%s", stdout)
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
	stdout, _, _ := env.exec(followCtx, "compose", "--workdir", wd, "-f", composePath, "logs", "--follow")

	for _, line := range []string{"alpha-stock", "beta-stock"} {
		if got := strings.Count(stdout, line); got != 1 {
			t.Fatalf("expected stored line %q exactly once (must not be duplicated by the live bridge), got %d:\n%s", line, got, stdout)
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
	if !strings.Contains(stdout, "started      alpha") ||
		!strings.Contains(stdout, "started      beta") {
		t.Fatalf("expected started lines for alpha and beta; got:\n%s", stdout)
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
