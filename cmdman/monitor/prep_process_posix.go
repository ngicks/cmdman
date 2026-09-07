//go:build !plan9 && !windows && !wasm

package monitor

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// startTty starts cmd attached to a fresh PTY and returns the master fd.
func startTty(cmd *exec.Cmd) (*os.File, error) {
	return pty.Start(cmd)
}

// prepCommandAttrs configures platform-specific exec attributes for a
// supervised command: a fresh session (hence its own process group) so signals
// reach grandchildren, and a Cancel hook that signals the whole group on ctx
// cancellation.
//
// Both the TTY and the pipe path get a session of their own so that leftovers
// from a run can be told apart from the monitor's other children by session
// id: a process can create a new session but can never join an existing one,
// so every descendant of the command stays outside the monitor's session
// unless it deliberately breaks away. Process group id gives no such
// guarantee.
//
// Setpgid must not be set alongside Setsid: exec runs setsid() first, and the
// following setpgid() is then rejected with EPERM on a fresh session leader.
// Nothing is lost, because a session leader is also the leader of its own
// process group, so signalling -pid still reaches the group. On the TTY path
// pty.Start sets Setsid (and Setctty) on the attrs it is handed, so setting it
// here only makes the intent explicit.
func prepCommandAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
}

// prepHookAttrs configures exec attributes for a hook process: its own process
// group, and the same group-wide cancellation contract as a supervised
// command.
//
// A hook deliberately stays in the monitor's session. A sweep that classifies
// run leftovers by session id therefore never mistakes a running hook for a
// process left behind by the command.
func prepHookAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
}

// signalProcessGroup sends sig to the process group led by pid. Returns nil
// if pid is non-positive (no group to signal).
func signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}
