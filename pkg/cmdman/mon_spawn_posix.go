//go:build !plan9 && !windows && !wasm

package cmdman

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
)

// This file implements the POSIX detach strategy for the per-command monitor:
// a double-fork built on setsid(2) (see forkMonitorDaemon). A Windows port
// would add a mon_spawn_windows.go providing its own SpawnMonitor
// and DaemonizeMonitor — detachment there is a single CreateProcess with
// DETACHED_PROCESS / CREATE_NEW_PROCESS_GROUP flags, so no second stage and no
// daemon marker are needed.

// envMonitorDaemon marks a re-exec'd monitor process as the final detached
// daemon — the grandchild of the double-fork. When it is set, the __monitor
// entry point runs the monitor loop directly instead of daemonizing again.
const envMonitorDaemon = "__CMDMAN_INTERNAL_MONITOR_DAEMON"

// SpawnMonitor launches the per-command monitor as a detached daemon.
//
// It re-executes the current binary's hidden __monitor command and waits for
// that process to return. That first __monitor process is the intermediate of
// a double-fork: it places itself in a new session (setsid) and forks
// the real monitor daemon (see DaemonizeMonitor), then exits. Waiting for it
// here reaps the intermediate and surfaces any daemonization error, while the
// daemon itself is reparented to init and keeps running.
//
// Detachment is owned by the monitor entry point, not this call site: this
// function only starts the binary and reaps the intermediate.
func SpawnMonitor(cfg CmdmanConfig, id string) error {
	cmd, err := newMonitorCmd(cfg, id, nil)
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start monitor: %w", err)
	}

	// The intermediate exits as soon as it has forked the daemon. Reaping it
	// here avoids a zombie and propagates any daemonization failure.
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("monitor failed to daemonize: %w", err)
	}
	return nil
}

// DaemonizeMonitor is the entry point for the hidden __monitor command. It
// implements the monitor side of the double-fork:
//
//   - First invocation (the intermediate spawned by SpawnMonitor): place the
//     process in its own session and fork the real daemon, then return so the
//     intermediate exits.
//   - Re-exec'd invocation (the daemon, marked by envMonitorDaemon): run the
//     monitor loop.
func DaemonizeMonitor(
	ctx context.Context,
	id string,
	cfg CmdmanConfig,
	logger *slog.Logger,
) error {
	if os.Getenv(envMonitorDaemon) == "1" {
		return RunMonitor(ctx, id, cfg, logger)
	}
	return forkMonitorDaemon(cfg, id)
}

// forkMonitorDaemon performs the double-fork from inside the intermediate
// __monitor process. It detaches into a new session, then re-execs the current
// binary as the real monitor daemon with stdio redirected to /dev/null and the
// envMonitorDaemon marker set. The daemon is released so it runs independently;
// because the intermediate returns right afterwards, the kernel reparents the
// daemon to init, and because the daemon is not a session leader it can never
// acquire a controlling terminal.
func forkMonitorDaemon(cfg CmdmanConfig, id string) error {
	// Detach into a brand-new session, becoming the session leader with no
	// controlling terminal. setsid(2) fails with EPERM if the caller is
	// already a process-group leader; this intermediate never is, since it is
	// a freshly exec'd child that inherited its parent's process group (its
	// PID differs from its PGID). The real daemon forked below is a child of
	// this leader, so it can never reacquire a controlling terminal.
	if _, err := syscall.Setsid(); err != nil {
		return fmt.Errorf("setsid: %w", err)
	}

	cmd, err := newMonitorCmd(cfg, id, []string{envMonitorDaemon + "=1"})
	if err != nil {
		return err
	}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start monitor daemon: %w", err)
	}

	// Run independently; the daemon is reparented to init once this
	// intermediate process exits.
	return cmd.Process.Release()
}
