# Compose Reconciliation State

## Current Status

Core reconciliation rewrite implemented and tested. `compose up` and spec-backed
`compose start` now converge through an explicit reconcile graph walked from
`begin`. All three stated failures are fixed and covered by unit and e2e tests.

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
  ordering, closure exclusion. Up-walk is covered here for future stop/down use.
- E2E (`e2e/cmdman/compose_test.go`): re-up exited, start-from-failed, started
  condition does not block on exit, completed re-run honors the new run,
  independent concurrency.
- Commands run (2026-05-30):
  - `go test ./pkg/cmdman/compose` → ok (also `-race` → ok)
  - `go test ./pkg/cmdman` → ok
  - `go test ./e2e/cmdman -run 'Compose|Start'` → ok
  - `go test ./...` → all ok **except** two pre-existing PTY/attach failures
    (`TestAttach_CtrlCRestoresShellTtyMode`,
    `TestAttach_ExitsWhenCommandStopsFromCtrlC`). Confirmed these fail identically
    on the clean baseline (git stash) and do not touch the compose path; they are
    environment-sensitive (WSL2 PTY), unrelated to this change.

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
- **Outcomes** stay deterministic in spec order; blocked vertices carry the last
  recorded wait reason as their error.

## Remaining / deferred

- **Production wiring of `stop`/`down` to `walkUp` (PLAN step 3, step 5 down
  action)**: deferred. The reusable graph and the up-walk are implemented and
  unit-tested, but `Service.Stop`/`Service.Down` still use the existing
  `TopoLayers` reverse-dependency ordering. Rationale: all three stated failures
  and the entire PLAN test plan concern up/start; `stop`/`down` already produce
  correct reverse-dependency ordering and are covered by ordering-sensitive tests
  (e.g. `TestComposeReverseDepOrderStop`), so rewiring them carries regression
  risk for no failure-fixing benefit. Adopting `walkUp` for stop/down (preserving
  orphan handling and the remove phase) is a clean follow-up on top of this graph.

## Notes

- Update this file whenever part of `PLAN.md` is implemented.
- Include tests run and any changed design decisions.
