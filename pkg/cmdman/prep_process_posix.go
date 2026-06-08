//go:build !plan9 && !windows && !wasm

package cmdman

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

// prepProcessAttrs configures platform-specific exec attributes for a
// monitored command: process-group placement so signals reach grandchildren,
// and a Cancel hook that signals the whole group on ctx cancellation.
//
// For tty=true, pty.Start sets Setsid=true on its own, which already places
// the child in a new session and (implicitly) a new process group. Setting
// Setpgid in addition would fail with EPERM during exec (setpgid is rejected
// on a session leader). So only set Setpgid for the non-TTY path; signals can
// be sent to the whole group via -pid either way.
func prepProcessAttrs(cmd *exec.Cmd, tty bool) {
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	if !tty {
		cmd.SysProcAttr.Setpgid = true
	}
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
