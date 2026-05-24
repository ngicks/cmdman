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
	stateDetail, _ := info["state_detail"].(map[string]any)
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
	if info["state"] != "failed" {
		t.Errorf("expected state=failed after monitor crash, got %v", info["state"])
	}

	stateDetail, _ = info["state_detail"].(map[string]any)
	errorMsg, _ := stateDetail["error"].(string)
	if errorMsg == "" {
		t.Error("expected error message in state_detail after stale detection")
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
	st.InsertCommandState(id, model.StateRunning, &model.CommandState{
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
