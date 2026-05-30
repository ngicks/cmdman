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

- `starting` or `started`: do not call `Start`; record success in the graph as
  an active command.
- `created`, `exited`, or `failed`: call `cmdman.Service.Start`.
- Start failures are recorded for that command and propagated only to its
  dependents.
- Sibling branches keep running unless the caller's context is canceled.
- Results stay deterministic in spec order.

For dependency conditions:

- `condition: started` is satisfied by pre-existing `starting/started`, or by a
  graph state update from the dependency during this reconciliation.
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

This observation is the service-level equivalent of `compose ps`: it should use
the same project/workdir/command selection semantics and expose the same current
state, exit code, and command identity that `compose ps` would show. Reconcile
decisions and user-visible status must agree on this snapshot.

Use `Command.GeneratedName` as the `Start` target to preserve current behavior,
but retain stored `ID`, `State`, and `ExitCode` for dependency decisions,
diagnostics, and tests.

### 2. Introduce an explicit reconcile graph

Add a graph type under `pkg/cmdman/compose`, replacing the start-only channel
sketch with a reusable graph walker for `up`, `start`, `stop`, and `down`.

The graph should include two virtual vertices:

- `begin`: every command depends on `begin`.
- `end`: `end` depends on every command.

These virtual edges are added for every command, not only roots/leaves. That
keeps graph construction uniform and removes special root/terminal detection
from the builder. A root command is a command whose only parent is `begin`. A
leaf command is a command whose only child is `end`.

For compose `after`, keep the natural edge direction as:

```text
dependency -> dependent
```

For example, if `worker.after.api.condition: started`, the graph has:

```text
begin -> api -> end
begin -> worker -> end
api --started--> worker
```

Independent services are simply multiple outgoing edges from `begin` and
multiple incoming edges to `end`.

Suggested internal shape:

```go
type vertexID string

const (
    beginVertex vertexID = "\x00begin"
    endVertex   vertexID = "\x00end"
)

type graphEdge struct {
    From      vertexID
    To        vertexID
    Condition AfterCondition
}

type graphVertex struct {
    ID       vertexID
    Command  *Command // nil for begin/end
    Snapshot commandSnapshot

    Parents  map[vertexID]graphEdge
    Children map[vertexID]graphEdge

    Queued     bool
    InProgress bool
    Consumed   bool
    Blocked    bool
    State    model.EventType
    ExitCode *int
    Err      error
}

type reconcileGraph struct {
    Vertices map[vertexID]*graphVertex
}
```

The graph state is in-process reconciliation state. It is initially populated
from the command snapshot and then updated by workers after each service action.
This lets later vertices make dependency decisions from the graph rather than
from stale store snapshots.

Cycle validation stays strict. Unlike Docker Compose, this project rejects
cyclic graphs; keep or reuse existing `ValidateDAG` behavior before building the
reconcile graph.

### 3. Add directional graph walks

Add two walk functions:

- `walkDown(ctx, graph, targets, action)`: start at `begin` and move toward
  `end`. Used by `compose up` and spec-backed `compose start`.
- `walkUp(ctx, graph, targets, action)`: start at `end` and move toward
  `begin`. Used by spec-backed `compose stop` and `compose down` stop phases.

The walkers share one work queue and use `errgroup.Group` as the only
concurrency limiter:

```go
type walkDirection int

const (
    walkFromBegin walkDirection = iota
    walkFromEnd
)

type graphAction func(context.Context, *reconcileGraph, *graphVertex) actionResult
```

Use a buffered channel sized to the number of target commands:

```go
workCh := make(chan vertexID, len(targetCommands))
```

Do not close `workCh`. The queue is fed from multiple scheduler paths, so the
implementation should not rely on close-channel cancellation unless there is one
obvious final writer. Instead, derive completion from graph state and cancel the
worker context when the walk is done.

Initial enqueue:

- down walk: enqueue command vertices with no real parents, i.e. whose only
  parent is the virtual `begin` edge.
- up walk: enqueue command vertices with no real children, i.e. whose only child
  is the virtual `end` edge.

The virtual nodes are not action targets. They only seed and terminate the walk.

Use `errgroup.Group.SetLimit(limit)` for bounded parallelism. Do not introduce a
semaphore or a separate fixed worker pool. Do not use worker errors to cancel
sibling branches; ordinary command failures are accumulated into graph vertices
and final outcomes.

The walker does not need a separate pending-work counter. Track scheduling state
on graph vertices (`queued`, `in_progress`, `consumed`, `blocked`) and use it to
answer the only termination question that matters: are all target vertices
finished or blocked, and are there no in-progress actions left? That state is
also what final reporting needs.

### 4. Consumption and enqueue rules

Each `errgroup` task must take the graph lock before deciding whether to act on
a vertex.
The same vertex can become reachable from multiple parents/dependents, so the
walk must be idempotent at the vertex level.

Task loop:

1. Receive or claim a vertex ID from the scheduler.
2. Lock graph state.
3. If the vertex is virtual, consumed, or not in the operation target closure,
   skip it.
4. Check whether the vertex is ready for this walk direction.
5. If not ready, leave the vertex pending without recording an error. Another
   parent/dependent completion will enqueue it again later. After the whole walk
   drains, any still-unconsumed target vertex is reported as blocked by unmet
   dependency/dependent conditions.
6. Mark it consumed or in-progress before unlocking, so duplicate enqueues do
   not run the action twice.
7. Run the action outside the lock.
8. Lock graph state and update vertex `State`, `ExitCode`, and `Err`.
9. Enqueue next vertices that may now be ready.

Sketch:

```go
for {
    var id vertexID
    select {
    case <-ctx.Done():
        return ctx.Err()
    case next := <-workCh:
        id = next
    }

    v, ready, skip := graph.claim(id, direction)
    if skip {
        graph.releaseSkipped(id)
        continue
    }
    if !ready {
        graph.markPending(id)
        continue
    }

    result := action(ctx, graph, v)

    next := graph.complete(v.ID, result, direction)
    for _, id := range next {
        enqueue(id)
    }
    graph.completeAction(v.ID)
}
```

`claim` is where consumption is checked and the vertex is marked in-progress.
`complete` is where graph state is updated and the next frontier is computed.
For a down walk, the next frontier is children; for an up walk, it is parents.
`releaseSkipped`, `markPending`, and `completeAction` update only graph
scheduling state and wake the scheduler. When graph state shows no queued or
in-progress target vertices remain, the scheduler marks any unconsumed target
vertices as blocked, records final outcomes, and cancels the worker context.
`workCh` remains unclosed.

`markPending` should record enough reason/state to be shown to users later, for
example "waiting for api to complete successfully" or "waiting for worker to
stop". Treat it as part of the reconcile status model, not only internal
bookkeeping.

Readiness checks are directional:

- Down walk readiness: all relevant parents satisfy their edge condition.
- Up walk readiness: all relevant children have already been consumed or are
  already in a state that makes stopping/removing this vertex valid.

For down walk, edge condition evaluation uses graph state:

- `begin -> command`: always satisfied.
- `started`: parent state is `starting` or `started`, or this reconciliation
  has just recorded `started`.
- `completed`: parent state is `exited` or `failed`.
- `completed_successfully`: parent state is terminal and exit code is `0`.

For up walk, the default condition is structural rather than `after.Condition`:
dependents are handled before dependencies. The exact stop action can still
decide that an already-stopped command is a no-op success.

If a vertex is ready but the operation cannot be performed, record an error on
that vertex and continue walking unrelated branches. For example:

- `completed_successfully` cannot be satisfied because a parent exited non-zero:
  record a dependency error on the dependent and do not start it.
- `Start` fails for a command: record a start error and do not enqueue its
  dependents whose conditions cannot be satisfied.
- `Stop`/`Remove` fails: record the action error, continue independent branches,
  and report the aggregate at the end.

### 5. Define operation actions

The walker should be generic; behavior lives in small action functions.

For `up/start` action:

- If graph state is `starting` or `started`, no-op success.
- If graph state is `created`, `exited`, or `failed`, call
  `cmdmanSvc.Start(ctx, GeneratedName)`.
- After `Start`, update graph state by observing the backing command. At minimum
  record `started` when `Start` returns nil. If the command already reached a
  terminal state before `Start` observed `started`, update graph state from the
  store or `Wait(stopped)` so `completed` edges can progress.
- If any outgoing edge requires `completed` or `completed_successfully`, wait
  for stopped and update `State`/`ExitCode` before enqueueing dependents.

For `stop` action:

- If state is already terminal, no-op success.
- Otherwise call `cmdmanSvc.Stop`.
- Update graph state from the stop result or by observing stopped state.

For `down` action:

- Walk up to stop dependents before dependencies.
- After stop phase completes, remove stopped commands. Removal can be a second
  graph walk or a concurrent pass over vertices that are terminal and selected.
- Preserve existing orphan handling rules.

### 6. Target closure rules

The graph should support operation-specific target closures:

- `up/start` with no command names: all commands.
- `up/start` with command names: named commands plus all recursive dependencies,
  because dependencies may need to run to satisfy `after`.
- `stop/down` with no command names: all commands.
- `stop/down` with command names: named commands plus recursive dependents, so a
  dependency is not stopped before commands that depend on it.

This target closure is separate from graph construction. Build the full graph
from the spec, then mark which vertices are in the operation closure.

### 7. Preserve parallelism

The scheduler must allow all currently-ready vertices to run concurrently up to
the `errgroup.Group.SetLimit` value:

- independent roots from `begin` start together;
- fan-out dependents become eligible together after their parent updates graph
  state;
- only true dependency paths serialize;
- stop/down naturally run in reverse dependency order by walking up from `end`.

Add a conservative default limit, for example `runtime.GOMAXPROCS(0)` or
`min(len(targetCommands), 8)`, pass it to `errgroup.Group.SetLimit`, and keep the
implementation structured so a CLI flag can be added later if needed.

### 8. Align spec-backed and project-only start

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

## Progress Tracking

After implementing any part of this plan, update
`doc/plan/compose-01/STATE.md` in the same change. Record what was completed,
what remains, relevant files touched, tests run, and any behavior decisions made
while implementing.

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
