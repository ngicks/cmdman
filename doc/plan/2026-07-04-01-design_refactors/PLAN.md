# Design improvements / refactors — survey, direction, and ranking

One-line summary: Survey the codebase for design-level improvements (seeded by three
maintainer-identified items), write a direction for each, and rank them into an ordered
refactor backlog.

## Goal / success criteria

- Every improvement candidate has: problem statement with file references, proposed
  direction, effort estimate, risk, and rank.
- The three seeded items are fully specified (concrete move lists, target packages).
- The ranking is agreed with the maintainer and can be executed item-by-item, each
  independently verifiable (build + `go test ./...` + e2e green after each).

## Scope / non-goals

- Scope: design direction and ordering. Individual items may later get their own plan
  directories (repo convention: `doc/plan/compose-NN`, `mux-NN`, ...).
- Non-goals: feature work, behavior changes visible to users, TUI feature work
  (tracked separately in `doc/plan/2026-06-26-01-tui_improvements`).

## Context

- Go CLI process manager. Command layer `cmd/cmdman/commands/` (cobra), library layer
  `pkg/cmdman/` (with `cli/`, `compose/`, `mux/`, `store/`, `tui/`, `eventlog/`,
  `logdriver/`, `model/` subpackages), mux abstraction `pkg/muxctl/` with tmux backend
  `pkg/muxctl/tmux/`. E2E tests in `e2e/cmdman/`.
- ~42k LOC of Go under `pkg/`.
- Per-file evidence below was gathered by four codebase scans (one per seeded item
  plus a general sweep) on 2026-07-04.

## Improvement candidates

### C1. Push `[compose] mux` subcommand logic down into `pkg/cmdman` (seeded item 1)

Problem: `cmd/cmdman/commands/mux.go` and `compose_mux.go` contain business logic
(resolution / orchestration) that belongs in the library layer. Keeping
`compose.MuxLeafResolver` exported is fine, but the compose service should expose Mux
methods so the command layer only parses flags and formats output.

Findings (scan):

- **The orchestration exists in three copies**: `runComposeMuxUp`
  (`compose_mux.go:233-301`), and near-identical `serviceBackend.muxRun`
  (`pkg/cmdman/cli/tui_backend.go:422-474`) plus a second selection resolver
  (`tui_backend.go:479-499` vs `compose_mux.go:496-511`). Consolidating into
  `compose.Service` methods lets both cmd and the TUI backend share one path.
- Established pattern the mux commands violate: `compose_up.go:35-75` /
  `compose_down.go:32-65` do load → build service → **one service method** →
  `cli.Print*`. `compose_mux.go` instead assembles resolver + scale state +
  `PaneArgvOpts` + `mux.Build`/`mux.Run` inline.
- Concrete move list (~150-200 lines out of compose_mux.go, ~15-20 out of mux.go,
  ~100 duplicate lines deleted from tui_backend.go):
  - `compose.Service.MuxUp` ← `compose_mux.go:247-300` + `tui_backend.go:427-474`
  - `compose.Service.MuxCycleScale` ← `compose_mux.go:407-444` (arg parsing of
    `<cmd>[=N]` stays in cmd)
  - `compose.Service.MuxLs` ← non-render part of `compose_mux.go:328-377`
    (returns a struct; cmd keeps `cli.RenderMuxWindows`)
  - MuxDown ← `compose_mux.go:306-326` (never touches `s.svc` — method or
    package-level func; kept as a Service method per D8)
  - `compose.ResolveMuxSelection` ← `resolveComposeMuxSelection` +
    `tui_backend.go`'s variant (careful: deliberately different auto-select
    behavior between CLI and TUI — must stay parameterized)
  - `mux.CollectCycleTargets` ← `compose_mux.go:522-553` (pure `mux.PaneSpec`
    walk, belongs beside the type)
  - `cmdman.Service.MuxResolver` ← inline resolver closure `mux.go:199-212`
- Hazards: `cmdmanSvc` consumer interface (`compose/service.go:16-29`) lacks
  `Config()`, needed for `PaneArgvOpts` — extend the interface or pass config in;
  `os.Executable()` called at all three sites — preserve error wording; new methods
  need a pluggable `io.Writer` (TUI passes `io.Discard`, CLI passes
  `cmd.OutOrStdout()`).
- No unit tests exist for this command-layer logic today (only e2e) — the move is
  low-risk for breakage and an opportunity to add unit tests under
  `pkg/cmdman/compose/`.

Direction (per D8): add `MuxUp`/`MuxDown`/`MuxLs`/`MuxCycleScale` as methods on
`compose.Service` (uniform surface, even though MuxDown/MuxLs barely touch the
underlying service), plus `ResolveMuxSelection`; rewire cmd and tui_backend to call
them; add mirror `cmdman.Service.MuxResolver` for standalone mux; add unit tests
for the new methods.

Effort: M. Risk: medium (three call sites with deliberate behavioral differences;
e2e suite is the safety net).

### C2. Adopt sqlc for SQL (seeded item 2)

Problem: hand-written SQL strings and manual row scanning in `pkg/cmdman/store/`.

Findings (scan):

- Driver: `modernc.org/sqlite` (pure Go), `pkg/cmdman/store/store.go:16-17`,
  `go.mod:22`. ~20 hand-written statements across 8 non-test files.
- **Ports cleanly (~13 statements)**: all of `command_config_store.go` (insert /
  get / `ResolveID`'s three static queries), `command_state_store.go`,
  `exit_history.go`, and schema-version bootstrap reads — static, parameterized,
  fixed shape.
- **Does not port as-is (3 sites)**:
  - `store/delete.go:6-20` `DeleteCommand` — `fmt.Sprintf` table-name loop. Trivial
    fix: three static `DELETE` queries.
  - `store/list.go:23-102` `ListCommands` and `list.go:104-127` `FindByLabels` —
    variable number of `AND json_extract(...) = ?` clauses (per-label) plus a
    conditional `IN` clause. sqlc cannot express N-ary dynamic predicates. Either
    keep these two hand-rolled alongside sqlc, or normalize labels into a
    `CommandLabel(ID, Key, Value)` table so filtering becomes joins sqlc can express.
- Migrations are Go-native: `schemaVersion` in a `DBConfig` table plus
  `map[int]func(*sql.Tx) error` (`store/internal/migrations/migrations.go:9-11`,
  `store/migrate.go`). sqlc doesn't run migrations, but needs a canonical
  `schema.sql` input — mirror `schema.go:75-113` DDL into one and keep them in sync
  (or make schema.go embed the .sql file).
- Domain data is stored as opaque JSON blobs (`model.CommandConfig`/`CommandState`
  marshaled into a `JSON` TEXT column) — sqlc only generates structs for the thin
  scalar columns; hand-marshaling stays either way, so the win is limited to scan
  boilerplate and compile-time-checked SQL.
- No codegen wiring exists (no Makefile/Taskfile, no `go:generate`, empty
  `.github/`); the only precedent is manual `buf generate` in `pkg/api`. sqlc needs
  a `sqlc.yaml`, `queries/*.sql` + `schema.sql`, a version pin (e.g. go tool
  directive), and a documented regen step.

Direction (per D3, D4): **broader persistence rework** — adopt sqlc for 100% of
queries:

- Static queries port directly; `DeleteCommand` becomes three static deletes.
- `ListCommands`/`FindByLabels` become static SQL via `json_each` over a
  JSON-object parameter (Option C in `label-query-options.md`; sqlc-parser gate
  prototype-verified with sqlc v1.31.1). No schema change, no dual-write; also
  fixes the `labelJSONPath` key-quoting quirk.
- Migration mechanism reworked (per D3, D9): embedded `.sql` migration files
  (embed.FS: `0001_init.sql`, `0002_created_at.sql`, ...) executed by the existing
  `DBConfig.SchemaVersion` per-version-transaction walker — no new dependency; the
  current Go migration func (`internal/migrations/v2.go`) becomes `.sql`.
- sqlc's schema input derives from the same `.sql` chain (single source of truth);
  pin sqlc via go.mod `tool` directive; document the regen step alongside the
  `buf generate` convention.

Effort: M-L (grew with the broader-rework decision). Risk: low-medium
(behavior-preserving; store tests + e2e cover it; generated param types need
override annotations — `CAST` or sqlc.yaml `overrides:`).

### C3. Hoist generic (non-tmux) logic from `pkg/muxctl/tmux/` up to `pkg/muxctl/` (seeded item 3)

Problem: backend-agnostic logic lives under the tmux driver package; additionally,
`pkg/cmdman/mux` bypasses the `muxctl.Session` abstraction and imports concrete
`tmux` functions for capabilities `pkg/muxctl/doc.go:42-55` documents as a "driver
contract" but never reified as a Go interface.

Findings (scan):

- **Tier 1 — pure functions, hoist mechanically, no interface needed**:
  - `computeChildCells` (`tmux/sizing.go:23-81`) — pure geometry on `[]muxctl.Size`,
    already unit-tested without tmux.
  - `pickFocus`, `parentDim`, `childDims` (`tmux/apply.go:288-317, 269-274,
    278-283`) — pure `PaneSpec`/`Direction` logic.
  - `recordSkipped` (`tmux/apply.go:204-212`) — pure tree walk.
  - `shouldReuseUnmarkedWindow` (`tmux/reuse.go:64-66`) — pure decision fn, tested
    standalone.
  - `decodeScalePositions`/`encodeScalePositions` (`tmux/scale_state.go:29-50,
    56-81`) — pure codec, but "scale" isn't a muxctl-vocabulary concept (doc.go
    never mentions it) — moves to `pkg/cmdman/mux` per D6.
- **Tier 2 — needs interface extraction first**:
  - Window enumeration: `OwnedWindow`/`ListOwnedWindows` (`tmux/list.go:10-154`)
    consumed concretely by `pkg/cmdman/mux/{list.go:81, down.go:109,
    cycle_scale.go:98,296}`. Hoisting means adding an enumerator interface to
    `muxctl` and reworking those call sites.
  - Cycle-scale primitives: `FindLeafPane`/`RespawnLeaf` (`tmux/leaf.go:105-148`),
    `ReadScalePositions`/`WriteScalePosition` (`tmux/scale_state.go:87-142`),
    consumed concretely by `cycle_scale.go`. Needs a "respawn one leaf" Session
    method and an abstract per-window key/value store.
  - `ApplyLayout` materialize/split core (`tmux/apply.go:42-265`) — generic
    recursive-descent algorithm interleaved with tmux CLI at the leaves; would need
    a driver primitive interface (`Split`/`SpawnLeaf`/`QuerySize`). Highest value,
    highest effort; not a first step.
- tmux is the only driver today (zellij/wezterm "planned" per doc.go), so tier-2
  interfaces would have exactly one implementation for now.

Direction (per D6, D7): tier 1 only — hoist the pure layout/tree functions to
`pkg/muxctl/` (new `layout.go` or similar; behavior-preserving move + export),
except the scale-position codec, which moves to `pkg/cmdman/mux` (scale cycling is
a cmdman concept, not muxctl vocabulary). All tier-2 interface extraction
(enumeration, cycle-scale primitives, ApplyLayout core) is deferred until a second
driver is real.

Effort: S. Risk: low (pure moves, tests move with them).

### C4. Extract Monitor into a subpackage of `pkg/cmdman`

Problem: `pkg/cmdman` is flat (~40 files) and conflates four responsibilities:
public `Service` API (`cmdman.go`, `cmdman_*.go`), Monitor internals (`mon.go`,
`mon_run.go`, `mon_server.go`, `mon_spawn*.go`, `mon_clean.go`,
`terminal_screen.go`, `terminal_state.go`, `ringbuffer.go`, `broadcaster.go`),
config (`config*.go`), and client-side attach `Session` (`attach_session.go`).
`RunMonitor`/`newMonitor` are reachable only from `mon_spawn_posix.go:69` and
`cmd/cmdman/commands/monitor.go:36` — the monitor machinery is private in practice.
Violates the repo's stated "one responsibility per package" rule.

Direction: extract Monitor + private machinery (terminal emulation, ring buffer,
broadcaster, posix process prep) into e.g. `pkg/cmdman/monitor`; `pkg/cmdman` keeps
Service + Config + Session.

Effort: L (touches ~15 files + tests). Risk: medium (wide blast radius; mechanical).

### C5. Replace PID-reuse-prone monitor liveness check with the existing flock

Problem: `CheckMonitorAlive`/`isStaleMonitor` (`pkg/cmdman/mon_clean.go:11-22,
89-91`) probe liveness via `os.FindProcess` + `Signal(0)` on the stored PID. If the
OS recycles the PID, a dead monitor reads as alive and the entry wedges as
`starting`/`running`. `Monitor.init()` (`mon.go:202-209`) already holds
`flock.TryLockExclusive` on the same PID-file path (`cfg.MonitorPIDPath(id)`),
released on exit — a non-blocking flock attempt is a PID-reuse-immune probe, and
`pkg/cmdman/internal/flock/flock.go:10-12` already exposes it.

Direction: switch `isStaleMonitor` to a try-flock probe with these semantics:
- lock held by another process (try-lock busy) → monitor **alive**;
- lock acquired (release immediately) or PID file absent → monitor **stale**;
- unexpected open/flock errors (permissions, I/O) → return the error (caller logs
  via the ctx logger) — do **not** silently classify as stale/failed.

Effort: S. Risk: low. This is closer to a latent bug fix than a refactor.

### C6. Unit-test `broadcaster[T]`

Problem: `pkg/cmdman/broadcaster.go` — the sole fan-out primitive behind every
Attach/Subscribe/state-change stream — has no direct test (only indirect coverage
via `mon_test.go`/e2e), despite a race-sensitive contract (subscribe/send/close
ordering, subscribe-after-close, drop-for-slow-consumer at `broadcaster.go:52-58`).
`ringbuffer.go` by contrast has its own test.

Direction: add a direct `-race` unit test. Effort: S. Risk: none.

### C7. Split `compose/load.go` into discovery and normalization files

Problem: `pkg/cmdman/compose/load.go` (843 lines) mixes file/project discovery
(`DiscoverFile`, `ListNamedProjects`, `ListMuxProjects`, ... `load.go:71-267`) with
normalize/validate (`LoadAndNormalize`, `Normalize`, `validateMux*`, ...
`load.go:280-843`) — two independent stages with little shared state.

Direction: split into `discover.go` / `normalize.go`, same package, no import
changes. Effort: S-M. Risk: low (pure file reorganization).

### C8. Extract the detach-key lexer from `cli/attach.go`

Problem: `pkg/cmdman/cli/attach.go` (573 lines) bundles attach orchestration,
raw-terminal/signal mechanics, and a self-contained detach-key-sequence lexer
(`detachKeyReader`, `parseDetachKeys*` — `attach.go:351-556`, ~200 lines, zero
dependency on the rest of the file).

Direction: move the lexer to `attach_detachkeys.go` (same package). Effort: S.
Risk: none.

### C9. Split `cli/tui_backend.go` by TUI tab

Problem: 791-line flat adapter spanning the three TUI tabs (Commands `:57-406`,
Compose `:108-203`, mux/Layout `:407-791`). Not a defect (its placement in `cli` is
deliberate, see `tui_backend.go:23-26`), just a keeps-growing file.

Direction: split into `tui_backend_commands.go` / `_compose.go` / `_mux.go`. Note:
C1 already deletes ~100 lines of its mux section — do C9 after C1 if at all.
Effort: S. Risk: none.

### C10. Log (don't swallow) cleanup errors in stale-entry auto-remove

Problem: `markMonitorDied` (`mon_clean.go:60-83`) silently drops `os.RemoveAll`
errors (lines 76, 79-80) while the analogous live-monitor path `maybeAutoRemove`
(`mon.go:372-383`) logs a warning. Caller `Service.List` (`cmdman_list.go:16-30`)
has `ctx` in scope for the repo's `contextkey` logger convention.

Direction: wire the context logger through and warn on failure. Effort: S. Risk:
none.

## Evaluation & ranking

Criteria: value (correctness first, then future-change leverage / duplication
removed), effort, risk, and dependency order. Ranking accepted per D2:

| Rank | Item | Why here | Effort / Risk |
|---|---|---|---|
| 1 | C5 flock liveness probe | Latent correctness bug, tiny fix | S / low |
| 2 | C1 compose mux push-down | Seeded; kills 3-way duplication incl. TUI backend | M / med |
| 3 | C3 tier-1 muxctl hoist | Seeded; mechanical, pure moves | S / low |
| 4 | C2 sqlc adoption | Seeded; compile-checked SQL, but win capped by JSON-blob design | M / low-med |
| 5 | C6 broadcaster test | Cheap insurance on a load-bearing primitive | S / none |
| 6 | C10 cleanup-error logging | Trivial consistency fix | S / none |
| 7 | C8 detach-key lexer split | Cheap, self-contained | S / none |
| 8 | C7 load.go split | Nice-to-have file hygiene | S-M / low |
| 9 | C4 monitor subpackage | Highest structural value but widest churn — schedule when quiet | L / med |
| 10 | C9 tui_backend split | Do after C1 shrinks it, if still needed | S / none |

Dependency notes: C9 after C1. C4 after C5/C6/C10 (they touch the same mon_*
files — land the small fixes first so the big move doesn't carry them). C3 tier 1
is independent of C1 despite both touching mux-adjacent code (different layers).

## Implementation steps

Per D1, this plan is a backlog document, not a work log:

1. Work items in the agreed ranking order (see STATUS.md execution backlog).
2. For each item as it's picked up, spin a per-item plan directory following repo
   convention (`doc/plan/<topic>-NN`), referencing this plan's candidate section.
3. Small items (C5, C6, C8, C10) may be executed directly without their own plan
   directory — each is a single verifiable change.

## Testing / verification

- Each executed item must keep `go build ./...`, `go test ./...`, and the e2e suite
  green; pure-move refactors should be behavior-preserving (verify via e2e).
- sqlc adoption additionally verified by comparing generated query behavior against
  existing store tests.

## Risks

- Move-refactors (C1, C3) churn import paths; risk of conflict with in-flight work.
- C2 residual risks (the dynamic-SQL expressibility question is settled — D4 chose
  json_each and the prototype gate passed):
  - sqlc's param type inference is weak for the json_each queries (`Labels
    interface{}`, `LabelCount string` in the prototype) — needs `CAST` annotations
    or sqlc.yaml `overrides:`, and review of what it infers for every ported query.
  - Behavior parity of the json_each rewrite vs the current `json_extract`-per-label
    builder (empty label set, duplicate keys, keys containing quotes) — cover with
    store tests before/after.
  - Migration `.sql` chain becomes sqlc's schema source of truth (D9) — drift
    between the chain and the schema sqlc sees breaks silently at generate time;
    keep `schema.go` embedding from the same files.
  - Generated code churn in review; regen step is manual (like `buf generate`) with
    no CI enforcement, so stale generated code is possible.

## Open questions

None — all resolved. See DECISION.md D1-D9.
