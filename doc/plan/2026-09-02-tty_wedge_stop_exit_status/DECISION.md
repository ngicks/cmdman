# Decisions — TTY monitor wedge and `stop` exit status

Entries are appended as questions resolve; stubs mark the ones still open.

## D1 — sweep and reap run survivors (decided 2026-09-02)

User chose, in chat: "Like for podman container with `--init`, it should reap
all orphans", asking whether it is possible without a special daemon. It is:
the monitor process is already per-command and long-lived, so it makes
itself the subreaper of its descendants with
`prctl(PR_SET_CHILD_SUBREAPER)` (Linux) and, at run end, terminates and
reaps every process the run left behind. No extra daemon, no PID namespace,
no privileges. Non-Linux POSIX degrades to signalling the run's process
group.

Rejected: ending the run and orphaning survivors (the earlier tentative
default; leaves ports/files/terminals held and stacks survivors across
restarts); a PID namespace (needs user namespaces or CAP_SYS_ADMIN, and a
different spawn path); cgroups (needs a delegated cgroup — a daemon or
systemd).

## D2 — exit-status rule covers stop, restart, rm, signal; opt-out flag (decided 2026-09-02)

User chose all four verbs and asked for an opt-out flag with a conventional
name. Rule: attempt every target, print one `verb ID: reason` line per
failure, exit 1 with `one or more <verb> operations failed` unless the
opt-out flag is set. Flag name is IDEA Q2 (tentative `--ignore-errors`).

Rejected: `stop` only (leaves three verbs with the same silent-0 defect).

## D3 — fix compose stop-result handling in this plan (decided 2026-09-02)

User chose to fix it here rather than ledger it. Every compose site that
calls `Service.Stop` and drops `[]StopResult`
(`cmdman/compose/service_stop.go:118`, `service_restart.go:192`,
`service_reconcile.go:241`) surfaces the first per-target error as the
outcome error; `stopForRecreate` (`service_create.go:304`) already does.

Rejected: HANDOFF → issue backlog.

## D4 — sweep policy: reap, SIGTERM, short grace, SIGKILL, reap (decided 2026-09-02)

User asked what tini does, then chose the gentler variant. What tini does
(README, krallin/tini): spawn one child, "wait for it to exit all the while
reaping zombies and performing signal forwarding", then "Tini exits as
well, with the exit code of the child process"; `-s`/`TINI_SUBREAPER`
registers it as a subreaper when it is not PID 1; `-g` forwards signals to
the child's process group. tini never kills survivors itself — when PID 1
exits, the kernel tears the pid namespace down with `SIGKILL`.

cmdman's equivalent has no namespace to tear down, so the monitor does the
killing: it is the subreaper; stop signals already go to the process group
(tini `-g`); when the child exits, the monitor reaps what is already dead,
sends `SIGTERM` to every remaining run descendant, waits a fixed
`orphanGrace` (2 s — routine call, a constant, not a config key), `SIGKILL`s
the rest, and reaps until nothing remains.

Rejected: `SIGKILL` at once (exact container parity; the user preferred to
give survivors a chance to flush); the command's stop signal + grace
(config coupling for no clear gain); a configurable grace (nobody asked).

## D5 — opt-out flag is `--ignore-errors` (decided 2026-09-02)

User chose `--ignore-errors` (make `-i`, rsync convention: keep going and do
not fail). Rejected: `--ignore` (podman: missing targets only, different
meaning); `--keep-going`/`-k` (make/ninja: keep going but still fail — the
new default).

## D6 — detach from the pty reader; no pollable-ptmx trick (decided 2026-09-02)

User recalled earlier research: making the ptmx pollable (dup +
`O_NONBLOCK` + `os.NewFile`) is not portable to BSD-like hosts, whose
kqueue netpoller does not handle tty devices reliably (Go's own
`os/file_unix.go:155-200` exclusions and silent blocking fallback; libuv's
macOS tty workaround). That is the premise `go-common/iopipe` was built on
("an operation already blocked in the underlying reader … is never
interrupted", `iopipe/doc.go`), and attach already lives by it. The monitor
follows the same rule: the run's end never joins the goroutine parked in
`ptmx.Read`; it closes the ptmx, waits a short bound for trailing output,
and detaches. The survivor sweep (D1/D4) is what actually ends the read on
Linux, by closing every slave. A stale reader is fenced off from the next
run by a per-run generation.

Rejected: dup + `SetNonblock` + `os.NewFile` (Linux-only wake-up); opening
the pty in-repo on a non-blocking fd (same kqueue limit); forking creack/pty
(same limit, plus a dependency to carry); `SyscallConn` ioctls at the
`Setsize`/`Getsize` sites (pointless once nothing depends on pollability).

## D7 — implement the proc-handle-race plan first; `runPgid` under its `procMu` (decided 2026-09-03)

User chose to sequence this plan behind
`doc/plan/2026-08-16-02-monitor_proc_handle_race/` and reuse its lock. That
plan's R3 is quoted verbatim in PLAN.md "Prerequisite plan". `runPgid` is
published with `cmd`, cleared after the sweep, read copy-under-lock.

Rejected: a standalone `atomic.Int64` (race-free but a fourth handle with a
different discipline next to three that the other plan is about to put
under one mutex); a boundary-ledger handoff of the guarding (leaves a known
race in a plan that is itself about wedges).

## D8 — `Setsid` for non-TTY children (decided 2026-09-03)

User chose `Setsid` for the supervised child on both the TTY and the pipe
path. Hooks are **not** covered: `execHook` shares `prepProcessAttrs`
today and keeps `Setpgid`, so a hook stays in the monitor's session (the
attrs helper splits into command/hook variants). The sweep then
classifies a reparented direct child as a run descendant iff its session id
differs from the monitor's — exact, because a process can create a new
session but never join an existing one; hooks stay in the monitor's
session. `kill(-pid)` keeps working: a session leader is also its group's
leader.

Rejected: keep `Setpgid` and classify by pgid — a descendant that only
`setpgid`s itself would look like a hook and survive the sweep.

## D9 — bounded drain wait of 1 s before detaching (decided 2026-09-03)

User chose a bounded wait: after the sweep, `waitFn` closes the ptmx and
waits up to `readerDrainWait` (1 s) for the reader to return so trailing
output lands in the log before the exit event; on expiry it logs one
warning and detaches.

Rejected: detach immediately (last bytes may land after the exit event).

## D10 — run-end anomalies are reported, not swallowed (decided 2026-09-03)

User asked that a timed-out drain wait be logged and reportable rather
than silently absorbed. Two anomalies qualify: the pty reader still parked
when `readerDrainWait` expires, and survivors still alive when the sweep's
hard bound expires. Each goes to three places: a warn line in the monitor
log; `Attrs` on the run's terminal event (`reader_detached=true`,
`survivors_unreaped=<n>`) so `cmdman events` shows it in the timeline; and
`CommandState.Warnings` (new JSON field, reset at the next start) so
`cmdman inspect` shows it for the latest run. Routine calls: attr names,
the `Warnings` field name, and reuse of the existing `Attrs` map rather
than a new event type (a new type would make every consumer that switches
on `Type` handle one more case for no extra information).

Rejected: monitor log only (nobody reads it unless already debugging);
a dedicated `warning` event type (see above); the `Error` field of
`CommandState` (means "the run failed", which these do not).
