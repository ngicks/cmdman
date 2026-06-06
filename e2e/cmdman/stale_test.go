package cmdman_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// TestStale_DetectedOnLs verifies that stale entries (where the monitor
// has died) are detected and marked as failed when running ls.
func TestStale_DetectedOnLs(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start a command.
	id := env.run(ctx, "run", "-n", "stale-target", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "stale-target", "running", defaultTimeout)

	// Get the monitor PID from inspect.
	info := env.inspectJSON(ctx, "stale-target")
	stateDetail, _ := info["StateJSON"].(map[string]any)
	monitorPID, _ := stateDetail["monitor_pid"].(float64)
	if monitorPID <= 0 {
		t.Fatal("could not get monitor PID")
	}

	// Kill the monitor process directly (simulating a crash).
	proc, err := os.FindProcess(int(monitorPID))
	if err != nil {
		t.Fatalf("find monitor process: %v", err)
	}
	proc.Kill()

	// Wait for the process to actually die.
	time.Sleep(500 * time.Millisecond)

	// Running ls should detect the stale entry and mark it failed.
	env.run(ctx, "ls", "-a")

	info = env.inspectJSON(ctx, "stale-target")
	if info["State"] != "failed" {
		t.Errorf("expected state=failed after monitor crash, got %v", info["State"])
	}

	stateDetail, _ = info["StateJSON"].(map[string]any)
	errorMsg, _ := stateDetail["error"].(string)
	if errorMsg == "" {
		t.Error("expected error message in state_detail after stale detection")
	}
}

// TestStale_DetectedOnStop verifies that stop can repair a command whose
// monitor died without updating the persisted state.
func TestStale_DetectedOnStop(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dbPath := filepath.Join(env.dataHome, "commands.db")
	st, err := store.OpenStore(ctx, dbPath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	id := "stale-stop-test-id"
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "sleep 300"},
		Dir:             env.dataHome,
		Env:             os.Environ(),
		RestartPolicy:   model.RestartPolicyNo,
		StopSignal:      model.DefaultStopSignal,
		ScrollbackBytes: 1024,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      filepath.Join(env.dataHome, "commands", id),
	}
	if err := st.InsertCommandConfig(id, "stale-stop", cfg); err != nil {
		t.Fatalf("insert command config: %v", err)
	}
	if err := st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
		MonitorPID: os.Getpid(),
		SocketPath: filepath.Join(env.runtimeDir, id, "monitor.sock"),
	}); err != nil {
		t.Fatalf("insert command state: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "stop", "stale-stop")
	if err != nil {
		t.Fatalf("stop stale command failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	info := env.inspectJSON(ctx, "stale-stop")
	if info["State"] != "failed" {
		t.Fatalf("expected state=failed after stop repaired stale command, got %v", info["State"])
	}
	stateDetail, _ := info["StateJSON"].(map[string]any)
	if errorMsg, _ := stateDetail["error"].(string); errorMsg == "" {
		t.Fatal("expected error message in state_detail after stale stop")
	}
}

// TestStale_AutoRemoveOnStale verifies that a stale command with --rm
// is automatically removed when detected.
func TestStale_AutoRemoveOnStale(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// We need to manually create a stale entry in the DB to test auto-remove on stale.
	// Use the store directly.
	dbPath := filepath.Join(env.dataHome, "commands.db")

	st, err := store.OpenStore(ctx, dbPath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Create a fake command that looks running but has a dead PID.
	id := "stale-auto-rm-test-id"
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "echo fake"},
		Dir:             env.dataHome,
		Env:             os.Environ(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 1024,
		LogDriver:       model.DefaultLogDriver,
		Annotations:     map[string]string{store.AnnotationAutoRemove: "true"},
		CommandDir:      filepath.Join(env.dataHome, "commands", id),
	}
	st.InsertCommandConfig(id, "stale-auto-rm", cfg)
	st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{
		MonitorPID: 99999999, // A PID that is almost certainly not alive.
	})
	st.Close()

	// Running ls triggers stale detection. Since the command has --rm annotation,
	// it should be auto-removed.
	env.run(ctx, "ls", "-a")

	// The command should be gone.
	entries := env.lsJSON(ctx)
	for _, e := range entries {
		if e["ID"] == id {
			t.Error("stale auto-rm command still present after ls")
		}
	}
}
