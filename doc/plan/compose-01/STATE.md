# Compose Reconciliation State

## Current Status

PLAN fully implemented. `compose up` and spec-backed `compose start` converge
through an explicit reconcile graph walked from `begin`; spec-backed
`compose stop` and the `compose down` stop phase now converge through the same
graph walked from `end` (the previously deferred PLAN step 3 `walkUp` / step 5
down action). All three stated failures remain fixed, and stop/down reverse-
dependency teardown is now graph-driven. Covered by unit and e2e tests.

## Completed

### 1. Command snapshot (PLAN step 1)
- Added `commandSnapshot{ID, GenName, State, ExitCode}` and
  `Service.snapshotCommands` (replaces `snapshotProjectStates`). It is the
  service-level equivalent of `compose ps`: same `projectLabels` selection, same
  state/exit-code/identity. `Command.GeneratedName` is still the `Start` target.

### 2-4, 7. Reconcile graph + directional walkers (PLAN steps 2-4, 7)
- New `pkg/cmdman/compose/reconcile.go`:
  - `vertexID`, `graphEdge`, `graphVertex`, `reconcileGraph` with virtual
    `begin`/`end` vertices; `begin->cmd` and `cmd->end` edges added uniformly for
    every command. Real edges keep `dependency -> dependent` direction with the
    dependent's `after.Condition`.
  - `buildReconcileGraph(spec, snaps, closure)` builds the full graph from the
    spec and marks closure membership separately (PLAN step 6).
  - Generic `walk(ctx, dir, limit, action)` with `walkFromBegin`/`walkFromEnd`.
    Scheduling is `claim` / `complete` / `seed` / `done` / `finalize` over an
    unclosed buffered channel sized to the command count. Completion is derived
    from graph vertex state (`Queued`/`InProgress`); blocked/never-ready targets
    are resolved by `finalize`. Bounded parallelism via `errgroup.SetLimit`; the
    worker context is cancelled when the walk is done.
  - Down-walk edge evaluation (`evalDownEdgeLocked`): an **in-closure** parent is
    judged by its current-run result (must be `Consumed`); an out-of-closure
    parent is judged by its pre-run snapshot. `markPending` records a wait reason
    surfaced to users on block.
  - Strict cycle validation (`ValidateDAG`) runs before building the graph.

### 5, 8. Up/start action + wiring (PLAN steps 5, 8)
- New `pkg/cmdman/compose/service_reconcile.go`:
  - `Service.reconcileStart` (shared by `Up` and `startWithSpec`): validate DAG →
    snapshot → build graph (closure = named + transitive deps) → walk down →
    collect outcomes in deterministic spec order.
  - `Service.upStartAction`: `starting`/`started` ⇒ no-op success; otherwise
    `Start`. When an in-closure dependent needs completion, `Wait(stopped)` and
    record the terminal state + exit code (never infer completion from `Start`,
    which returns nil even if the command exits before `started` is observed).
- `service_up.go` / `service_start.go` now call `reconcileStart`. Spec-backed
  start matches project-only start and low-level `cmdman start`: start
  `created`/`exited`/`failed`, skip only `starting`/`started`.
- Removed `service_start_dag.go` (channel-based `startInDAGOrder`,
  `waitForCondition`, `depEvent`, `dagCommand`, `anyAwaitsCompletion`,
  `checkExitZero`). `resolveTargetCommands` moved to `reconcile.go`.

### 3 (walkUp), 5 (down), 6 (stop closure). Stop/down via the reconcile graph

- `reconcile.go` additions:
  - `resolveStopTargetCommands(spec, names)`: stop/down closure = named commands
    **plus recursive dependents** (walks the dependent direction), so a
    dependency is never stopped while a command that depends on it still runs
    (PLAN step 6). names empty ⇒ all commands.
  - `stopOutcomes(spec)`: per-command `StopOutcome` ordered by a new monotonic
    consumption sequence (`graphVertex.Order`, stamped under lock in `complete`
    and `finalize`). For an up walk this is teardown order: dependents before
    dependencies — what `PrintStopResult`/`PrintDownResult` render.
- `service_reconcile.go` additions:
  - `Service.reconcileStop` (shared by `Stop` and the `Down` stop phase):
    validate DAG → snapshot → build graph (stop closure) → `walk(walkFromEnd)`
    → `stopOutcomes`.
  - `Service.stopAction`: only `starting`/`started` (with a known ID) are
    stopped via `cmdmanSvc.Stop`; `created`/`exited`/`failed` are no-ops (a stop
    on them only returns monitor-connect errors). Failures are recorded per
    command; the up-walk readiness ignores child state, so a dependent's stop
    failure never blocks stopping its dependency (best-effort teardown).
- `service_stop.go`: spec path now calls `reconcileStop`; removed the dead
  `stopLayerConcurrent`. No-spec path unchanged (`stopAllConcurrent`).
- `service_down.go`: spec stop phase calls `reconcileStop`. New helpers
  `runningEntries`, `stopOrphans`, `filterEntriesInClosure`. Remove set: whole
  project (incl. orphans) when no names; exactly the named+dependents closure
  when names are given. No-spec path unchanged. Orphan stop/remove rules
  preserved (down is destructive whole-project teardown).
- `TopoLayers`/`reverseLayers`/`buildIDByCommand` are still used by
  `service_restart.go` and `plan.go`, so they stay.

## Failures fixed

1. **Stale terminal state** — in-closure dependencies are evaluated against the
   run this reconciliation owns (parent must be `Consumed`), never the snapshot.
   Because the up/start closure pulls in all transitive deps, `after` is always
   judged against the current run. (Unit:
   `TestReconcileStaleTerminalStateDoesNotSatisfyCompleted`; e2e:
   `TestComposeUpReRunHonorsCompletedDependency`.)
2. **Restart from terminal states** — `created`/`exited`/`failed` get `Start`.
   (Unit: `TestReconcileRestartsExitedAndFailed`; e2e:
   `TestComposeUpRestartsExitedCommand`, `TestComposeStartFromFailedState`.)
3. **Parallelism preserved** — independent roots run concurrently (limit
   `min(len(closure), 8)`); only dependency edges serialize. (Unit:
   `TestReconcileIndependentCommandsStartConcurrently`,
   `TestReconcileFailedBranchDoesNotBlockSibling`; e2e:
   `TestComposeUpIndependentCommandsStartConcurrently`.)

## Tests

- Unit (`service_internal_test.go`): started/completed/completed_successfully
  conditions, stale-state fix, restart of exited/failed, skip-active, concurrent
  independent start via barriers, failed-branch isolation.
- Graph (`reconcile_test.go`): virtual-edge construction, down-walk and up-walk
  ordering, closure exclusion.
- Stop walk (`service_internal_test.go`, fake `cmdmanSvc.Stop` recorder added):
  `TestReconcileStopVisitsDependentsBeforeDependencies` (teardown order +
  outcome order), `TestReconcileStopSkipsNonRunning`,
  `TestReconcileStopWithNamesIncludesDependents` (named pulls in dependents,
  excludes unrelated), `TestReconcileStopContinuesPastFailedDependent`
  (dependent stop failure recorded but dependency still stopped; logger injected
  via `contextkey.WithSlogLogger`).
- E2E (`e2e/cmdman/compose_test.go`): re-up exited, start-from-failed, started
  condition does not block on exit, completed re-run honors the new run,
  independent concurrency; existing `TestComposeReverseDepOrderStop` still green
  under the graph walk; new `TestComposeStopByNameStopsDependents`.
- Commands run (2026-05-30):
  - `go test ./pkg/cmdman/compose` → ok (also `-race` → ok)
  - `go test ./pkg/cmdman` → ok
  - `go test ./e2e/cmdman -run 'Compose|Start'` → ok
  - `go test ./...` → all ok **except** the same two pre-existing PTY/attach
    failures (`TestAttach_CtrlCRestoresShellTtyMode`,
    `TestAttach_ExitsWhenCommandStopsFromCtrlC`). They are environment-sensitive
    (WSL2 PTY) and touch only the attach path, not compose; unrelated to this
    change.

## Behavior / design decisions

- **Concurrency model**: chose an unclosed buffered work channel drained by
  `errgroup`-managed workers (bounded by `SetLimit`) over per-vertex `eg.Go`.
  PLAN step 4's mechanics (claim / markPending / re-enqueue on later completion /
  unclosed channel / completion derived from graph state) describe exactly a
  worker-loop model; that is internally at odds with the "no fixed pool, SetLimit
  only" note, and the channel mechanics were taken as authoritative. The result
  honors both bounded parallelism and the described scheduling.
- **Completed edges** use `Wait(stopped)` and map a recorded exit code to
  `exited` (any code) vs. `failed` (no code). `completed_successfully` requires
  `exited` with code 0 from this run's observation, never a stale exit code.
- **Up/start outcomes** stay deterministic in spec order; blocked vertices carry
  the last recorded wait reason as their error.
- **Stop/down outcomes** are reported in consumption (teardown) order via
  `graphVertex.Order`, not spec order, so the user-visible summary matches the
  reverse-dependency order things were actually stopped in. This keeps
  `TestComposeReverseDepOrderStop` (asserts `worker` before `api` in stdout)
  meaningful under the graph walk.
- **Stop/down closure expansion is a deliberate behavior change**: `stop`/`down`
  with command names now also acts on the named commands' recursive *dependents*
  (PLAN step 6), unlike the prior code that touched only the named commands.
  There was no existing test asserting the old "only-named" behavior; the new
  behavior is covered by `TestReconcileStopWithNamesIncludesDependents` and
  `TestComposeStopByNameStopsDependents`.
- **`stopAction` records `EventTypeExited` on a successful stop** purely for
  diagnostics; the up-walk readiness ignores vertex state and `stopOutcomes`
  reads only `Err`, so the recorded state is never user-visible. Kept simple
  rather than re-observing the precise stopped state.

## Remaining / deferred

- Nothing outstanding from PLAN.md. All eight implementation steps and both
  test-plan groups are implemented. `down`'s remove phase remains a concurrent
  pass over the resolved target set (PLAN step 5 explicitly allows either a
  second graph walk or a concurrent pass over selected terminal vertices); the
  concurrent pass was kept as it is simpler and already correct.

## Notes

- Update this file whenever part of `PLAN.md` is implemented.
- Include tests run and any changed design decisions.
