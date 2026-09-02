---
tags: cli stop exit-status bug
---

# `cmdman stop` exits 0 after per-target failures

`runStop` (`cmd/cmdman/commands/stop.go:64-68`) prints each `result.Err`
to stderr and then returns nil, so `cmdman stop a b c` exits 0 even when
one target failed (for example the TTY monitor wedge tracked in
`tty-monitor-wedges-when-grandchild-holds-pty-slave.md`). A script chaining
`stop && rm` cannot detect it. Found on 2026-09-02 while investigating that
wedge.

Fix direction: return a non-nil error (or set a non-zero exit status) when
any result carries an error, matching how `rm` reports refusals.
