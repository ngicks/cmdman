package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLauncherDown_ComposeDownOutlivesTheLauncher is what running the teardown
// as a job of its own is for. The launcher's `D`y starts the job and follows
// it, and a launcher that goes away mid-teardown, as a floating pane closed by
// hand does, leaves the job running: the project's commands are still taken
// away, and the job's record says it got all the way through. A launcher opened
// afterwards opens on the line the finished teardown left.
//
// The project's one command takes three seconds to stop, so the launcher is
// killed while the teardown is still stopping it. It is killed only once it
// says the teardown is running: before that the job may not exist yet, and the
// kill would race the launch rather than the teardown.
func TestLauncherDown_ComposeDownOutlivesTheLauncher(t *testing.T) {
	requireTmux(t)
	ctx := testContext(t)
	env := newTestEnv(t)

	tmuxTmpdir := t.TempDir()
	t.Cleanup(func() { killDefaultTmuxServer(t, tmuxTmpdir) })
	tmuxRunWithTmpdir(t, tmuxTmpdir, "new-session", "-d", "-s", "keep", "-n", "home")

	wd := composeWorkdir(t)
	const project = "lnchjob"
	ready := filepath.Join(t.TempDir(), "ready")
	composePath := writeComposeFile(t, wd, slowStopYAML(project, ready))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })
	t.Cleanup(func() { cleanupDownJob(ctx, env, project) })

	// up records the project in history, so the launcher opens on it.
	if _, stderr, err := env.muxExecWithTmpdir(
		ctx, tmuxTmpdir, "compose", "--workdir", wd, "-f", composePath, "up",
	); err != nil {
		t.Fatalf("compose up failed: %v\nstderr:\n%s", err, stderr)
	}
	waitUntil(t, defaultTimeout, func() bool {
		return strings.Contains(readFile(t, ready), "ready")
	}, "the command never set its TERM trap")

	w := startWidgetCmd(t, ctx,
		widgetCmd(env, wd, t.TempDir(), "launcher").WithTmuxTmpdir(tmuxTmpdir))
	w.waitFor(t, project, 10*time.Second)
	w.Send("\r") // enter: the input hands the keyboard to the locations list
	w.Send("D")
	w.waitFor(t, project+"? y/n", 10*time.Second)
	w.Send("y")
	w.waitFor(t, "compose down "+project+": running", 20*time.Second)

	if err := syscall.Kill(w.Pid(), syscall.SIGKILL); err != nil {
		t.Fatalf("kill the launcher: %v", err)
	}
	if _, exited := w.WaitWithin(t, 5*time.Second); !exited {
		t.Fatal("the launcher outlived SIGKILL")
	}
	if ids := composeCommandIDs(ctx, t, env, wd, project); len(ids) == 0 {
		t.Fatal("the teardown was over before the launcher was killed, which proves nothing")
	}

	waitUntil(t, 20*time.Second, func() bool {
		return len(composeCommandIDs(ctx, t, env, wd, project)) == 0
	}, "the project's commands outlived the launcher that asked for their teardown")

	job := downJobEntry(ctx, t, env, project)
	id, _ := job["ID"].(string)
	env.waitForState(ctx, id, "exited", 20*time.Second)
	if code := env.inspectJSON(ctx, id)["ExitCode"]; code != float64(0) {
		t.Errorf("the job exited with %v, want 0", code)
	}

	again := startWidgetCmd(t, ctx,
		widgetCmd(env, wd, t.TempDir(), "launcher").WithTmuxTmpdir(tmuxTmpdir))
	again.waitFor(t, "compose down "+project+": stopped 1, removed 1", 20*time.Second)
	again.quitWith(t, "\x03")
}

// slowStopYAML is a project of one command that takes three seconds to stop.
// It writes "ready" to readyPath once its TERM trap is set, and the trap kills
// the sleep it waits on before exiting, so nothing of it is left for the
// monitor's sweep to wait out.
func slowStopYAML(project, readyPath string) string {
	// The script goes through compose interpolation, so a $ the shell is to see
	// is written $$.
	script := fmt.Sprintf(`trap 'sleep 3; kill $$! 2>/dev/null; exit 0' TERM
sleep 300 &
echo ready >> "%s"
wait`, readyPath)
	return fmt.Sprintf(`name: %s
commands:
  slow:
    args: [sh, -c, %q]
`, project, script)
}

// downJobEntry returns the record of project's compose down job, failing the
// test unless there is exactly one.
func downJobEntry(
	ctx context.Context,
	t *testing.T,
	e *testEnv,
	project string,
) map[string]any {
	t.Helper()
	entries := e.lsJSON(ctx,
		"-l", "cmdman.compose.job=down",
		"-l", "cmdman.compose.job.project="+project,
	)
	if len(entries) != 1 {
		t.Fatalf("compose down jobs of %s = %v, want one", project, entries)
	}
	return entries[0]
}

// cleanupDownJob removes project's compose down jobs. A job is not
// auto-removed: its record keeps the result of the teardown for whoever asks
// next.
func cleanupDownJob(ctx context.Context, e *testEnv, project string) {
	// Detached for the reason cleanupProject is: it runs from t.Cleanup.
	ctx = context.WithoutCancel(ctx)
	entries, _, _ := e.exec(ctx, "ls", "-a",
		"-l", "cmdman.compose.job=down",
		"-l", "cmdman.compose.job.project="+project,
		"--format", "{{.ID}}",
	)
	for id := range strings.FieldsSeq(entries) {
		e.exec(ctx, "rm", "-f", id)
	}
}
