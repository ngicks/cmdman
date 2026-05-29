# Compose Reconciliation Fix Plan

## Problem

`cmdman compose up` and `cmdman compose start` do not currently converge command
state correctly around `after` dependencies, prior terminal states, and
parallelism.

Failures to fix:

1. `after` conditions must be observed against the run that this reconciliation
   is responsible for. A dependent must not proceed from stale terminal state
   when the dependency is being started again.
2. `compose up/start` must start commands that are in `created`, `exited`, or
   `failed` state. Only `starting` and `started` are already active.
3. Reconciliation must remain concurrent. Independent commands should start at
   once; only dependency edges should wait.

Relevant code today:

- `pkg/cmdman/compose/service_up.go` calls `Create`, snapshots only
  `map[command]state`, then calls `startInDAGOrder`.
- `pkg/cmdman/compose/service_start.go` does the same for spec-backed
  `compose start`; project-only start already starts entries concurrently.
- `pkg/cmdman/compose/service_start_dag.go` is the main target. It has a
  goroutine per command and per-command event channels, but dependency fast
  paths use only pre-operation state and cannot distinguish old terminal state
  from the current reconciliation's run.
- `pkg/cmdman/cmdman_start.go` already allows low-level `Start` from `created`,
  `exited`, and `failed`.

## Desired Semantics

For every selected command in spec-backed `up/start`:

- `starting` or `started`: do not call `Start`; record success and publish a
  started dependency event.
- `created`, `exited`, or `failed`: call `cmdman.Service.Start`.
- Start failures are recorded for that command and propagated only to its
  dependents.
- Sibling branches keep running unless the caller's context is canceled.
- Results stay deterministic in spec order.

For dependency conditions:

- `condition: started` is satisfied by pre-existing `starting/started`, or by a
  started event from the dependency during this reconciliation.
- `condition: completed` is satisfied by relevant terminal state:
  pre-existing terminal state only when the dependency is outside the active
  reconciliation set, otherwise the terminal event from the new run.
- `condition: completed_successfully` additionally requires exit code `0`.
  Non-zero, absent exit code, start failure, or wait failure blocks only the
  dependent branch.
- A command pulled in as a transitive dependency participates like any other
  selected command; it is not a synthetic/no-op node.

## Implementation Plan

### 1. Snapshot command records, not just states

Replace `snapshotProjectStates` with a compose-local snapshot type:

```go
type commandSnapshot struct {
    ID       string
    GenName  string
    State    model.EventType
    ExitCode *int
}
```

Build `map[composeName]commandSnapshot` from
`List(AllStates: true, Labels: projectLabels(...))`.

Use `Command.GeneratedName` as the `Start` target to preserve current behavior,
but retain stored `ID`, `State`, and `ExitCode` for dependency decisions,
diagnostics, and tests.

### 2. Make DAG events represent lifecycle observations

In `service_start_dag.go`, replace boolean `depEvent` fields with an event that
can carry the observed state:

```go
type depEvent struct {
    Type     model.EventType // started, exited, failed
    ExitCode *int
    Err      error
}
```

Each selected command goroutine should:

1. Wait for every dependency edge condition.
2. If the snapshot state is `starting/started`, publish `started`, record
   success, and return unless completion wait is needed for outgoing edges.
3. Otherwise call `Start`.
4. After successful `Start`, publish `started`.
5. If any outgoing edge requires completion, call `Wait(stopped)` and publish
   the terminal event with exit code.

This preserves prompt unblocking for `started` edges while supporting
`completed` and `completed_successfully`.

### 3. Do not reuse stale terminal state for restarted dependencies

Change `waitForCondition` so pre-existing `exited/failed` can satisfy
`completed` only when the dependency is not active in this reconciliation.

If the dependency is selected or pulled in transitively and is not already
`starting/started`, dependents must wait for the new run's events. This is the
core fix for stale success/failure being reused across `up/start`.

### 4. Decouple branch errors from global cancellation

Use an `errgroup.Group` or equivalent goroutine join for lifecycle management,
but do not use `errgroup.WithContext` for ordinary command/dependency failures.
The only global cancellation source should be the caller's `ctx`.

Branch failures should:

- record a `StartOutcome` for the failed command;
- publish an error event to dependents;
- allow unrelated branches to continue.

### 5. Preserve event-driven parallelism

Keep one goroutine per selected command. Commands wait only by reading their
dependencies' event channels.

Expected behavior:

- independent commands start concurrently;
- fan-out dependents unblock together;
- dependency chains serialize only along the chain;
- `completed` edges wait only for the depended-on command's terminal event, not
  for an entire topological layer.

`TopoLayers` can remain for stop/restart/down flows. `up/start` should stay
event-driven because edge conditions differ.

### 6. Align spec-backed and project-only start

Spec-backed `compose start` should match project-only start and low-level
`cmdman start`:

- call `Start` for `created`, `exited`, and `failed`;
- skip only `starting` and `started`;
- surface low-level errors for invalid or unexpected states.

Do not treat `failed` or `exited` as "already done" for the command being
started. They are only dependency facts when the dependency is outside the active
reconciliation set.

## Test Plan

Add focused unit tests in `pkg/cmdman/compose/service_internal_test.go` with a
fake `cmdmanSvc` that records `Start` calls and controls `Wait` results:

- `started` dependency starts dependent after dependency start event without
  waiting for terminal state.
- `completed` dependency waits for terminal event before starting dependent.
- `completed_successfully` blocks dependent on non-zero exit code.
- selected commands in previous `exited` and `failed` states call `Start` again.
- independent commands call `Start` concurrently using channels/barriers instead
  of sleeps.
- one failed branch does not prevent an independent branch from starting.

Add e2e coverage in `e2e/cmdman/compose_test.go`:

- `compose up` on commands that already exited starts them again and increases
  exit history.
- `compose start` after a failed command starts it again once the executable is
  fixed, matching existing `cmdman start` behavior.
- `condition: started` with a long-running dependency starts the dependent
  promptly while the dependency is still running.
- `condition: completed` with a marker file verifies dependent starts only after
  dependency exits.
- independent slow commands both begin before either finishes, asserted via
  marker files or logs rather than timing alone.

Run:

```sh
go test ./pkg/cmdman/compose
go test ./pkg/cmdman
go test ./e2e/cmdman -run 'Compose|Start'
go test ./...
```

## Risks And Notes

- `cmdman.Service.Start` returns nil if the command exits before
  `WaitForState(started)` observes `started`. Completion edges must therefore
  call `Wait(stopped)` and must not infer terminal success from `Start` alone.
- `completed_successfully` must use the exit code from the relevant terminal
  observation, not a stale pre-run exit code when the dependency was restarted.
- Keep CLI output behavior unchanged; service code should still aggregate
  per-command outcomes for `pkg/cmdman/cli/compose.go`.
- Do not move business logic into `cmd/cmdman/commands`; changes should stay
  under `pkg/cmdman/compose` and existing service boundaries.
