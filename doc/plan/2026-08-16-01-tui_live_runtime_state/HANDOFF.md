# Handoff — tui_live_runtime_state

Ledger of work leaving this plan. Entries are out-of-scope discoveries
or user-approved deferrals only.

## Pre-existing monitor data race (out-of-scope discovery, step 1)

**Picked up 2026-08-16 by
[../2026-08-16-02-monitor_proc_handle_race/](../2026-08-16-02-monitor_proc_handle_race/PLAN.md).**

`Monitor.QueueStdin` (`cmdman/monitor/mon.go:421`), `SignalProcess`
(`mon.go:464`) and `GetState` (`mon.go:484`) read `m.stdin` / `m.cmd`
unguarded while `runOnce` nils both after `cmd.Wait()`
(`cmdman/monitor/mon_run.go:213-215`) without taking `stdinMu`. Any
in-process test that issues a `WriteStdin` / `Signal` / `Status` RPC
and then lets the run end trips `-race`; `go test ./cmdman/monitor/
-race` is clean today only because no existing in-process test combines
the two. Discovered while building
`cmdman/cmdman_runtime_state_watch_test.go` (its comment at lines 47-52
records the workaround: marker-file staging + a second subscription
instead of `Status` polling).

Not fixed here: monitor-side changes are this plan's explicit non-goal
("`WatchRuntimeState` ... ship as-is"). Follow-up: a future
monitor-focused change should add a `stdinMu`-guarded accessor for
`m.cmd`/`m.stdin` (or equivalent) and a regression test combining an
RPC with a run ending.
