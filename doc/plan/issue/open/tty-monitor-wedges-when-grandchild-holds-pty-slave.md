---
tags: monitor pty stop tty lifecycle bug
---

# TTY monitor wedges in "running" when a grandchild keeps the pty slave open

Symptom, seen repeatedly and seemingly unrelated to the command being run:

```
$ cmdman stop $(cmdman ls -aq)
stop 5d2c…: timeout waiting for stop, and SIGKILL failed: rpc error: code = Unknown desc = no running process
$ cmdman rm $(cmdman ls -aq)
rm 5d2c…: command is running, use --force to remove
```

The state stays `running` for good, a second `stop` fails at once with
`no running process`, and the monitor process is alive throughout.
Reproduced on v0.0.24 and `main` (2026-09-02); non-TTY commands are
unaffected.

## Mechanism

Verified via `/proc` on a wedged monitor; killing the survivor released it
within a second.

1. `pty.Setsize`/`pty.Getsize` flip the pty master into blocking mode:
   creack/pty does its ioctls through `f.Fd()`
   (`creack/pty@v1.1.24/ioctl.go:9`), and `os.File.Fd()` deliberately calls
   `pfd.SetBlocking()`. The monitor calls `pty.Setsize` right after every
   TTY start to apply the 80x24 default (`cmdman/monitor/mon_run.go:248`),
   plus `Monitor.Resize` (`cmdman/monitor/mon.go:437`) and
   `Monitor.PtySize` (`mon.go:454`), so every TTY command's ptmx is
   blocking from run one (`fdinfo` shows no `O_NONBLOCK`).
2. The ptmx reader goroutine in `writeTty` (`mon_run.go:255-266`) is
   therefore parked in a real `read(2)`, which returns only when every fd
   on the slave side is closed (EIO); the thread sits in `wait_woken`.
3. After `cmd.Wait` returns, `runOnce` sets `m.cmd = nil`
   (`mon_run.go:218`) and calls `waitFn()` (`mon_run.go:220`), which is
   `ptmx.Close()` + `wg.Wait()` (`mon_run.go:275-276`). On a blocking-mode
   fd, `(*os.File).Close` neither waits for in-flight I/O nor closes the
   descriptor until the blocked read drops its reference
   (`internal/poll/fd_unix.go`: `if fd.isBlocking == 0 {
   runtime_Semacquire(&fd.csema) }`). So Close returns, the fd stays open,
   the read stays blocked, and `wg.Wait()` never returns.
4. `InsertCommandExitCode`/`setExited` are never reached, the gRPC server
   keeps serving, and the store keeps saying `running`.
5. The CLI's SIGKILL fallback (`cmdman/cmdman_stop.go:109-111`) cannot
   rescue it: `Monitor.SignalProcess` returns `no running process` when
   `m.cmd == nil` (`mon.go:463-465`) before reaching `signalProcessGroup`,
   so the survivor in the child's process group is never signaled.

`stop` only makes it visible: a TTY command that exits on its own while a
helper it spawned still holds the pty (a detached dev-server worker, an
agent, a language server, anything nohup/setsid-style or ignoring SIGHUP)
also sits in `running` forever. A plain `cmd &` grandchild does not trigger
it because the child is the session leader and its exit SIGHUPs the
same-pgrp job; the survivor must ignore TERM and HUP or have left the
session.

## Reproduction

No `setsid` needed:

```
cmdman run -t -n tty-holder -- sh -c 'trap "" TERM HUP; sleep 300 & trap - TERM HUP; exec sleep 300'
cmdman stop -t 3 tty-holder   # timeout ..., SIGKILL failed: ... no running process
cmdman ls -a                  # running
cmdman rm tty-holder          # command is running, use --force to remove
```

## Fix direction

Two independent defects:

- Keep the ptmx pollable: issue `TIOCSWINSZ`/`TIOCGWINSZ` through
  `ptmx.SyscallConn().Control` at all three call sites instead of
  `pty.Setsize`/`pty.Getsize`, so `ptmx.Close()` wakes the reader via the
  netpoller regardless of who holds the slave.
- Remember the run's pgid and let `SignalProcess`/`StopProcess` fall
  through to `signalProcessGroup` while the run has not finished, so the
  SIGKILL fallback actually ends a lingering slave holder.

Consider a bounded wait in `waitFn` that logs where the monitor is stuck,
and add an e2e test around the reproduction above.
