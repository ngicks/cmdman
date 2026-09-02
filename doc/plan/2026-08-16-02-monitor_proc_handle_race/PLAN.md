# Plan: guard the monitor's per-run process handles

Fix the pre-existing data race ledgered by the tui_live_runtime_state
plan's HANDOFF: RPC-facing `Monitor` methods read the per-run process
handles (`m.cmd`, `m.stdin`, `m.ptmx`) unguarded while `runOnce` nils
them after `cmd.Wait()` without taking any lock.

Status: **finalized 2026-08-16 — idea phase skipped (R1), no open
questions.** Successor to
[../2026-08-16-01-tui_live_runtime_state/](../2026-08-16-01-tui_live_runtime_state/HANDOFF.md);
its HANDOFF entry is quoted below and marks this plan as its pick-up.

## Inherited handoff (quoted, not summarized)

From the parent HANDOFF.md, verbatim:

> `Monitor.QueueStdin` ..., `SignalProcess` ... and `GetState` ... read
> `m.stdin` / `m.cmd` unguarded while `runOnce` nils both after
> `cmd.Wait()` ... without taking `stdinMu`. Any in-process test that
> issues a `WriteStdin` / `Signal` / `Status` RPC and then lets the run
> end trips `-race` ... Follow-up: a future monitor-focused change
> should add a `stdinMu`-guarded accessor for `m.cmd`/`m.stdin` (or
> equivalent) and a regression test combining an RPC with a run ending.

## Goal / success criteria

1. `go test ./cmdman/monitor/ -race` stays clean **including** a new
   regression test that combines `Status` / `WriteStdin` / `Signal`
   RPCs with a run ending — the exact combination that trips `-race`
   today.
2. No behavior change: an RPC arriving after the run ended still gets
   the same "no stdin" / "no running process" / pid-0 answers.
3. The workaround constraint recorded in
   `cmdman/cmdman_runtime_state_watch_test.go` (marker-file staging
   because `Status` polling trips the race) is lifted and its comment
   updated to say so.

## Scope

- `cmdman/monitor/mon.go`, `cmdman/monitor/mon_run.go` (and the posix
  wiring files if they assign the handles).
- A regression test in `cmdman/monitor/`.
- The stale workaround comment in
  `cmdman/cmdman_runtime_state_watch_test.go:47-52` (comment only; the
  test's staging mechanism stays — it is good regardless).

## Non-goals

- Any change to RPC semantics, restart policy, or runtime-state
  streaming (`WatchRuntimeState` and friends ship as-is).
- `outputMu`-guarded state (`logWriter`, `terminalState`, `screen`) —
  already correctly guarded, untouched.
- `runtimeState` — carries its own lock by design
  (`mon_run.go:219-224` comment), untouched.

## Context (merged main, 23fa0b0)

- The handle fields and the existing mutex: `cmdman/monitor/mon.go:54-58`
  — `ptmx *os.File`, `stdin io.WriteCloser`, `stdinMu sync.Mutex`,
  `cmd *exec.Cmd`.
- Unguarded writers in `runOnce` (`cmdman/monitor/mon_run.go`):
  `m.cmd = cmd` at `:208`; `m.ptmx = nil; m.stdin = nil; m.cmd = nil`
  at `:213-215` right after `cmd.Wait()`. The assignments of `ptmx` /
  `stdin` happen inside `writeTty` / `wirePipe` (`:236`, `:277`) —
  before the command starts, but still on the run goroutine.
- Unguarded readers (`cmdman/monitor/mon.go`), all reachable from gRPC
  handlers while the run goroutine is in/around `Wait()`:
  - `QueueStdin:413` — reads `m.stdin` at `:420` *before* taking
    `stdinMu` (the double-check at `:425` is also a race, because the
    writer never locks);
  - `Resize:433` and `PtySize:450` — read `m.ptmx`;
  - `SignalProcess:463` (and `StopProcess:471` through it) — reads
    `m.cmd`;
  - `GetState:477` — reads `m.cmd` at `:484` for the pid.
- The HANDOFF names `stdin`/`cmd`; `ptmx` is the same family (same
  writer line, same reader pattern) and is covered here so the fix is
  not half-done.

## Approach

One mutex owns the trio. Rename `stdinMu` to `procMu` (it no longer
guards only stdin) and route every access through it:

- `runOnce` takes `procMu` to publish the handles once wiring is done
  (`ptmx`/`stdin` set by `writeTty`/`wirePipe`, `cmd` at start) and to
  clear all three after `Wait()`. Two short critical sections — the
  lock is never held across `Wait()` or any Write.
- Readers take `procMu` to *copy* the handle out, release, then act on
  the copy: signaling a pid, `pty.Setsize`/`Getsize`, reading the pid
  in `GetState`. `QueueStdin` keeps holding `procMu` across the
  `Write` as it already does with `stdinMu` — that also keeps
  concurrent stdin writes serialized, which is current behavior.
- Acting on a copied handle after the process died is already the
  pre-fix best case (the race merely made it undefined); the OS-level
  answers (`ESRCH`, write-on-closed) map to the same error returns.

Rejected alternatives: per-field atomics (three related fields updated
together — a single mutex is simpler and the sections are tiny);
`outputMu` reuse (different concern, and `QueueStdin` holding it
across a blocking `Write` would stall output fan-out).

## Public surface delta

None. Every touched symbol is unexported inside `cmdman/monitor`; no
CLI, config, RPC, or persistent format changes.

## Implementation steps

1. **Guard the handles.** Rename `stdinMu` → `procMu`
   (`mon.go:56`); publish/clear `ptmx`/`stdin`/`cmd` under it in
   `runOnce` (`mon_run.go:208,213-215` and the assignments inside
   `writeTty`/`wirePipe`); convert the readers
   (`QueueStdin`/`Resize`/`PtySize`/`SignalProcess`/`GetState`) to
   copy-under-lock. Verify: `go build ./...`,
   `go test ./cmdman/monitor/ -count=1 -race` clean, and behavior
   answers unchanged (existing tests).
2. **Regression test.** In-process monitor test that starts a short
   run and hammers `Status` + `WriteStdin` + `Signal` RPCs across the
   run's end (the HANDOFF's trigger), under `-race`. Marker-file
   staging per `cmdman_runtime_state_watch_test.go`'s pattern where
   run timing must be controlled. Verify: test fails (races) with step
   1 reverted, passes with it — record the mutation check.
3. **Lift the workaround note.** Update the comment at
   `cmdman/cmdman_runtime_state_watch_test.go:47-52`: `Status` polling
   no longer trips the race; the marker staging stays because it makes
   the test deterministic, not because it dodges the race. Verify:
   `go test ./cmdman/ -count=1 -race` clean.
4. **Close out.** Mark the parent HANDOFF entry picked up (link this
   plan); keep STATUS.md current; full `go test ./... -count=1` +
   lint before done.

## Testing and verification

Step 2's regression test is the core deliverable-proof (criterion 1);
existing monitor tests pin criterion 2; step 3's package run pins
criterion 3. Final: full suite + golangci-lint.

## Risks

- `QueueStdin` holding `procMu` across a blocking stdin `Write` can
  delay a concurrent `SignalProcess` until the write returns — current
  behavior already serializes writers under `stdinMu`, and a PTY write
  blocks only against a wedged reader; accepted, noted here so the
  reviewer sees it was considered.
- Handle-copy-then-act windows (signal a pid that just died) — same
  window exists today minus the UB; OS errors map to existing error
  returns.

## Open questions

None — R1 (DECISION.md) records the user-directed skip of the idea
phase; the fix design is the HANDOFF's own follow-up made concrete.

## Downstream dependency (added 2026-09-03)

`doc/plan/2026-09-02-tty_wedge_stop_exit_status/` sequences its step 7
behind this plan (its D7) and adds a fourth handle, `runPgid`, under the
`procMu` this plan introduces. Boundary ledger, mirrored from that plan:

| Deliverable | Owner |
| --- | --- |
| `procMu` over `ptmx`/`stdin`/`cmd`; copy-under-lock readers | this plan, step 1 |
| Regression test: RPCs across a run's end under `-race` | this plan, step 2 |
| `runPgid` field, publish/clear under `procMu`, `SignalProcess` fallback | tty_wedge plan, step 7 |
