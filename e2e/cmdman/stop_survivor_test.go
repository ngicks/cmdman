package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// ttySurvivorScript spawns a process that outlives the supervised command and
// keeps the pty slave open. The background sleep inherits the ignored TERM/HUP
// dispositions, so neither the stop signal nor the hangup that follows the
// command's own exit clears it; the shell then restores its dispositions so the
// supervised process itself still stops on the first signal.
func ttySurvivorScript(pidFile string) string {
	return fmt.Sprintf(
		`trap "" TERM HUP; sleep 300 & echo $! > %s; trap - TERM HUP; exec sleep 300`,
		pidFile,
	)
}

// TestStop_TtySurvivorHoldsSlave pins the end of a tty run against a process
// that holds the slave open and refuses the signals a stop sends: the monitor
// sweeps what the run left behind, so the state reaches exited promptly instead
// of the command staying pinned in running.
func TestStop_TtySurvivorHoldsSlave(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	pidFile := filepath.Join(t.TempDir(), "survivor.pid")
	env.run(ctx, "run", "-t", "-n", "tty-holder", "--",
		"/bin/sh", "-c", ttySurvivorScript(pidFile))
	// Not ctx: cleanup runs after the test's context is already cancelled.
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "tty-holder") })

	env.waitForState(ctx, "tty-holder", "running", defaultTimeout)

	survivor := readPidFile(t, pidFile)
	t.Cleanup(func() { killIfAlive(survivor) })

	start := time.Now()
	env.run(ctx, "stop", "-t", "3", "tty-holder")
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("stop took %s; the pty holder wedged the end of the run", elapsed)
	}

	env.waitForState(ctx, "tty-holder", "exited", defaultTimeout)

	// rm without --force only succeeds on a terminal command, so it is what
	// separates a stop the store recorded from one the CLI merely reported.
	env.run(ctx, "rm", "tty-holder")

	waitUntil(t, 5*time.Second, func() bool { return !processExists(survivor) },
		"survivor pid %d is still in /proc; the run did not sweep it", survivor)
}
