# mux-02 — Decouple replica cycling from layout cycling; `compose mux cycle-scale`

Status: approved, not yet implemented.
Follows: mux-01 (window identity stamp; `mux up` / `mux down` / `mux ls`).

## Problem

`compose scale` creates per-replica commands (`<base>-<idx>`, labeled with
`cmdman.compose.scale-index`), but the mux dashboard does not treat scaled
commands as first-class. Replica cycling is **fused into layout cycling**:
`mux.Build` (`pkg/cmdman/mux/build.go`) expands every cmdman-layer layout that
contains unpinned scaled leaves into one pseudo-layout per replica-cycle
position (`name`, `name#2`, `name#3`, …), and `mux.Run`'s single window marker
walks through *all* of them on successive invocations.

Consequences:

- **No addressability.** There is no way to say "advance `web` to its next
  replica" or "show `web`'s replica 3". The only knob is "next thing", where
  a "thing" is an (layout, scale-position) pair.
- **Lockstep.** All unpinned leaves in a layout advance together
  (`(scalePos % n) + 1` in `buildPane`); two scaled commands cannot be
  inspected independently.
- **Layout switching degrades.** With L layouts and a max replica count of N,
  reaching "the next layout" takes up to N invocations; the cycle length is
  the *sum* over layouts of their per-layout cycle lengths.
- **Marker instability.** The persisted window marker indexes the *expanded*
  layout list, whose length depends on **live** replica counts. After
  `compose scale web=5` the expanded list reshuffles under the stored marker,
  so the next cycle lands somewhere arbitrary.
- **Name pollution.** Pseudo-layout names (`main#2`) leak into `--layout`
  selection, `mux ls`'s LAYOUT column, and completion.

## Design

### Core: two independent cycles

1. **Layout cycling** stays where it is: `mux.Run` cycles `Spec.Layouts` via
   the per-pane `@cmdman_marker` option. After this plan, `Build` emits
   **exactly one** `muxctl.Layout` per spec layout (the pseudo-layout
   expansion is deleted), so the marker is a stable 1:1 index into the spec's
   layouts again.
2. **Replica cycling** becomes per-window, per-command state advanced only by
   the new subcommand:

       cmdman compose mux cycle-scale <command>      # advance to next replica
       cmdman compose mux cycle-scale <command>=<N>  # jump to replica N (1-based)

   Exactly one argument is required. The `<name>=<N>` form mirrors
   `compose scale web=3`.

A leaf in the `mux:` section is a **cycle-scale target** iff it pins no
`scale:` (i.e. `PaneSpec.Scale == 0`, today's "unpinned"). A leaf with
`scale: N` is pinned and never cycles — `cycle-scale` does not touch it.

### State: window-level scale positions

Per-command positions are stored on the dashboard window as a new
window-level tmux user option (sibling of `@cmdman_window` from mux-01):

    @cmdman_scale = "<cmd>=<pos> <cmd>=<pos> ..."

- Space-joined `name=pos` pairs; compose command names are `[A-Za-z0-9._-]`
  (`validateName`, `pkg/cmdman/compose/load.go:680`), so the encoding is
  unambiguous.
- Window-level: survives pane churn, layout cycling, and `ApplyLayout`'s
  window reset. Positions therefore **persist across layout switches** —
  cycling to another layout keeps showing `web`'s replica 3 wherever `web`
  appears unpinned.
- Cleared by `Session.Detach` (`mux down`) alongside `@cmdman_window`, so a
  fresh dashboard starts at position 1 for every command.
- Encode/decode lives in `pkg/cmdman/mux`; the driver stores the map
  semantically (read/write a `map[string]int`), not the raw string — same
  layering as the identity (opaque below the driver boundary is not needed
  here because the driver must merge single-key updates).

A position absent from the map means 1. A stored position larger than the
live replica count (the command was scaled down since) is wrapped as
`((pos-1) % n) + 1` at read time, never errored.

### Pane ↔ leaf correlation: per-pane cycle-key stamp

`cycle-scale` must find which pane(s) currently display a command without a
full window rebuild. Pane titles are presentation, not identity (mux-00/01
lesson), so a new **per-pane** user option carries the link:

    @cmdman_leaf = <command>

- Set by `ApplyLayout` only on leaves that are cycle-scale targets (unpinned
  + cycling applies); cleared on every other pane. Carried into the driver
  via a new `muxctl.Leaf` field (e.g. `CycleKey string`, empty = not a
  target).
- `cycle-scale` lists panes by this option to locate the pane to respawn.
  Within one realized layout at most one pane can carry a given command
  (duplicate resolved IDs are rejected by `buildPane`'s `seen` map).

### `cycle-scale` flow (new `pkg/cmdman/mux` operation)

`CycleScale(ctx, CycleScaleOptions)` with options: built spec inputs
(`mux.Spec`, `Resolver`, `ReplicaCounter`, `PaneArgvOpts`), `Identity`,
`SessionName` (narrowing filter, like `Down`), `Command`, `Position`
(0 = advance by one).

1. **Static target check.** Error unless some layout in the spec contains an
   unpinned leaf for `Command` ("not a cycle-scale target").
2. **Find windows** by identity, server-wide (`tmux.ListOwnedWindows`), with
   `--session` narrowing. Zero matches → error:
   `no dashboard window found; run "cmdman compose mux up" first`.
   Multiple matches: operate on every match, one report line per window
   (mirrors `down`; multiple dashboards per project is not an envisioned
   state).
3. Per window:
   a. Read the layout marker → the current spec layout. Marker `-1` or out
      of range → error for that window (stale/foreign window).
   b. Read `@cmdman_scale`; current position = stored (wrapped) or 1.
      Live replica count `n` = `ReplicaCounter(Command)`.
   c. Compute the target position:
      - advance: `cur+1` wrapped to `[1..n]`, **skipping** indices pinned by
        other leaves of the same command in the current layout (a collision
        would violate one-pane-per-replica); if every index is pinned, error.
      - explicit `=N`: validate `1 <= N <= n` (live clamp); error if N is
        pinned by another leaf in the current layout.
   d. Resolve the replica: `Resolver(Command, target)` — the existing
      compose resolver errors when the replica does not exist live.
   e. Locate the pane via `@cmdman_leaf == Command`.
      - Pane present: quiesce its viewer (pane-scoped detach keys, same
        sequence `ApplyLayout` uses window-wide), retitle to
        `<command>-<target>`, respawn with the leaf's argv
        (`paneArgv(opts, leaf.Mode, id)` using the current layout's leaf
        `Mode`/`CmdOpt`).
      - No pane (current layout has no unpinned leaf for the command):
        still update the stored position and report
        `advanced to <target> (not visible in layout "<name>")` —
        consistent with positions persisting across layouts.
   f. Write the new position into `@cmdman_scale`.

`cycle-scale` never creates a window and never changes the layout marker.

### `up` flow: feeding positions into Build

`Build` gains a `ScalePositions map[string]int` input (1-based; missing key
= 1; wrapped against the live count as above) and loses the pseudo-layout
expansion: each unpinned cycling leaf resolves at its command's position,
`Leaf.CycleKey` is set, and exactly one `muxctl.Layout` per spec layout is
emitted. The `#%d` suffix naming disappears.

Callers (`runComposeMuxUp`, TUI `CycleMux`) read the positions **before**
building: discover the project's window by identity (reuse the
`ListOwnedWindows` path) and read `@cmdman_scale`; no window → empty map →
everything at replica 1. The read-then-apply gap is an accepted benign race
(interactive tool, single user).

Standalone `cmdman mux up` passes a nil `ReplicaCounter` and an empty
position map — behavior unchanged (unpinned leaves resolve at index 0;
pinned `scale: N` still resolves `<command>-<N>`). No standalone
`mux cycle-scale` in this plan.

### `ls`: SCALE column

`mux ls` / `compose mux ls` gain a SCALE column showing each window's
cycle-target positions and replica counts:

    SESSION  WINDOW  ID  IDENTITY      LAYOUT  SCALE
    main     cmdman  @3  aabb...-myapp 1       web=2/3 worker=1/2

- The positions come from the window's `@cmdman_scale` option. Reading it is
  free: `ListOwnedWindows` adds `#{@cmdman_scale}` to its existing
  `list-windows -F` format string (window options expand there) — no extra
  tmux call.
- The `/<count>` part is the **live** replica count. `compose mux ls`
  resolves it via the project's replica listing (the `ReplicaCounter`
  source), which means `compose mux ls` now opens the cmdman store — a new
  dependency for `ls` (today it needs only the spec + tmux). Commands whose
  count cannot be resolved (replica missing live) render `pos/?`.
- Cycle-target commands with no stored position render at the default
  (`web=1/3`), derived from the spec's unpinned leaves, so the column is
  informative even before the first `cycle-scale`.
- Standalone `mux ls` has no replica counter; it renders stored positions
  only (`web=2`). Windows with no cycle targets render `-`.

### Static validation: `mux:` against `commands:`

`compose.Normalize` (`pkg/cmdman/compose/load.go:303`) currently passes
`raw.Mux` through untouched (load.go:495). Add a validation pass when
`raw.Mux != nil`, after commands are normalized; for every leaf:

- the leaf's `command` must name a command in `commands:` (unknown name →
  error with the layout name and pane path);
- a pinned `scale: N` must satisfy `N <= <command's normalized Scale>`
  (absent `scale:` in `commands:` normalizes to 1), e.g.:
  `mux: layout "main": leaf "web": scale 3 exceeds commands.web.scale 2`.

This is spec-vs-spec only. Live divergence (ephemeral `compose scale`
overrides) is handled at resolution time: the existing compose `Resolver`
already errors on a missing live replica, and `cycle-scale` adds the live
clamp described above.

## Changes by package

### 1. `pkg/muxctl` + `pkg/muxctl/tmux`

- `spec.go`: `Leaf.CycleKey string` — the cycle-scale key (command name);
  empty = not a cycle target.
- `apply.go` (`realizeLeafAt`): set per-pane option `@cmdman_leaf` from
  `CycleKey` (unset when empty), next to the existing title/marker handling.
  Constant `leafOption = "@cmdman_leaf"`.
- New window-level scale-state primitives (e.g. `scale_state.go`):
  `ReadScalePositions(ctx, windowID) (map[string]int, error)` /
  `WriteScalePosition(ctx, windowID, cmd string, pos int) error` over the
  `@cmdman_scale` option (read-modify-write of the space-joined encoding).
  Constant `scaleOption = "@cmdman_scale"`.
- New pane lookup: `FindLeafPane(ctx, windowID, cycleKey) (paneID, ok, err)`
  via `list-panes -F '#{pane_id}\t#{@cmdman_leaf}'`.
- `list.go` (`ListOwnedWindows`): append `#{@cmdman_scale}` to the format
  string; `OwnedWindow` gains `ScalePositions map[string]int` (decoded via
  the shared scale-state codec).
- New `RespawnLeaf(ctx, paneID, leaf muxctl.Leaf)`: pane-scoped viewer
  quiesce (factor the send-keys part of `quiesceViewers` to take a pane id),
  then the existing title/option/respawn sequence of `realizeLeafAt` —
  share the implementation rather than duplicating it.
- `detach.go` (`Session.Detach`): unset `@cmdman_scale` alongside
  `@cmdman_window`; `@cmdman_leaf` dies with the panes (per-pane), but unset
  it in the detach pane-cleanup loop for symmetry with `@cmdman_marker`.
- `muxctl.Session` interface: only `ApplyLayout` consumers change via the
  `Leaf` field; the new primitives are driver-level functions/methods used by
  `pkg/cmdman/mux` directly (as `ListOwnedWindows` already is). Note the
  driver-portability caveat (user options are tmux-specific) in the same
  place `markerOption` documents it.

### 2. `pkg/cmdman/mux`

- `build.go`: delete `cyclingReplicaCounts`-driven expansion
  (`buildLayout` returns one layout; the `#%d` naming goes away). New input
  `ScalePositions map[string]int` (signature change of `Build`, or a
  `BuildOptions` struct — prefer the struct, `Build` already has 5 params).
  Unpinned cycling leaves resolve at the command's (wrapped) position and
  get `CycleKey` set. Pinned/non-cycling leaves: unchanged, no `CycleKey`.
- New `cycle_scale.go`: `CycleScale(ctx, CycleScaleOptions)` implementing the
  flow above; `ScaleState` encode/decode helpers; a small
  `ReadScaleState(ctx, opts) (map[string]int, error)` helper shared by the
  `up` callers (window discovery + option read).
- `list.go` (`List`): pass `OwnedWindow.ScalePositions` through to the row
  type consumed by the renderers.
- `run.go`: docs only (cycling text no longer mentions replica rotation).
- `spec.go` (`PaneSpec.Scale` doc): rewrite — unpinned now means "cycled by
  `cmdman compose mux cycle-scale`", not "cycles on successive `mux`
  invocations".

### 3. `pkg/cmdman/compose`

- `load.go` (`Normalize`): the static `mux:`-vs-`commands:` validation pass
  (own helper, e.g. `validateMux(spec.Mux, commandsByName)`).
- `service_mux.go`: unchanged (the resolver/counter already provide the live
  lookups `CycleScale` needs).

### 4. `cmd/cmdman/commands` (use the `go-edit-cobra` skill)

- `compose_mux.go` (`runComposeMuxLs`): open the cmdman service to resolve
  live replica counts for the SCALE column (counts keyed by the spec's
  unpinned leaf commands); keep the listing itself working when the store
  has no entries (counts render `?`).
- `compose_mux.go`: new `cycle-scale` subcommand under `compose mux`:
  `Use: "cycle-scale <command>[=N]"`, `cobra.ExactArgs(1)`, `--session`
  narrowing flag wired like `down`'s; parse the optional `=N` (reuse the
  `compose scale` arg-parsing shape); completion offers the spec's unpinned
  leaf command names. Root-alias note: `compose mux cycle-scale` is a real
  subcommand, so a layout literally named `cycle-scale` joins the
  `up`/`down`/`ls` shadowing edge in the help text.
- `runComposeMuxUp`: read scale positions (the new mux helper) before
  `mux.Build`; pass them through `BuildOptions`.
- `mux.go` (standalone): `Build` call-site signature update only.

### 5. `pkg/cmdman/cli`

- `tui_backend.go` (`CycleMux`): same read-positions-then-build update as
  `runComposeMuxUp`; share the helper, don't duplicate. No new TUI key.
- Result rendering for `cycle-scale` (one line per window:
  `<session>:<window> <command> -> <command>-<N>[ (not visible in layout
  "<name>")]`) lives here per the layering rule.
- `RenderMuxWindows`: new SCALE column (`cmd=pos/count` pairs, sorted by
  command name; `cmd=pos` without counts; `-` when the window has no cycle
  targets) + the matching `--format` template field; update
  `MuxLsFormatUsage`.

### 6. Docs

Primary:

- `doc/man/cmdman-compose-mux.1.md`: new `cycle-scale` subcommand section;
  rewrite the cycling description (layout cycle vs replica cycle; positions
  persist across layouts, reset by `down`); the `=N` form; errors (no
  window, not a target, replica out of range).
- `doc/man/cmdman-mux.5.md`: `scale:` semantics rewrite (pinned vs
  cycle-target; the per-invocation replica rotation is gone); document the
  `commands:`-bound constraint for the compose context.
- `doc/man/cmdman-compose.5.md`: `mux:` section — the static validation rule
  (`scale:` in `mux:` must not exceed the command's `scale:` in
  `commands:`; leaf must name a declared command).
- `doc/man/cmdman-mux.1.md`, `doc/man/cmdman-compose.1.md`: cycling wording
  + cross references.
- `ls` column lists (`cmdman-mux.1.md`, `cmdman-compose-mux.1.md`, and the
  `--format` usage text): add SCALE, including the compose-vs-standalone
  difference (`pos/count` vs `pos`) and the new store dependency of
  `compose mux ls`.

Sweep: `grep -rn 'cycle\|#2\|scale' doc/man/cmdman-*mux*` at the end of the
docs pass; also `example.compose.yaml` comments if they mention rotation.

## Tests

- `pkg/cmdman/mux` (unit, fake resolver/counter):
  - `Build` emits exactly one layout per spec layout; no `#%d` names.
  - Positions map honored; missing key → replica 1; stored 5 with live
    count 3 → wraps to 2; `CycleKey` set only on unpinned cycling leaves.
  - Scale-state encode/decode round-trip; hostile/empty option strings.
  - `CycleScale` position arithmetic: advance wrap, pinned-index skip,
    all-pinned error, explicit out-of-range error, explicit-pinned error,
    not-a-target error.
- `pkg/cmdman/compose` (unit): `Normalize` mux validation — unknown leaf
  command; pinned scale > command scale; pinned scale == command scale OK;
  absent `scale:` (cycle target) never errors statically.
- `pkg/muxctl/tmux` (real tmux, per-test socket):
  - `ApplyLayout` stamps/clears `@cmdman_leaf` per `CycleKey`.
  - Scale-state read/write/merge; `Detach` clears `@cmdman_scale`.
  - `FindLeafPane` + `RespawnLeaf` replace the pane process and retitle.
  - `ListOwnedWindows` returns the decoded scale positions per window.
- `e2e/cmdman/mux_test.go`:
  - Project with `scale: 3` on a command + unpinned mux leaf: `compose mux
    up` shows replica 1; `cycle-scale web` → pane shows `web-2`;
    `cycle-scale web=3` → `web-3`; wrap back to 1.
  - Layout cycle (`compose mux up`) after `cycle-scale` keeps the replica
    position (persist-across-layouts).
  - `cycle-scale` with no dashboard window → error mentioning
    `compose mux up`.
  - `compose mux ls` shows the SCALE column: `web=1/3` after `up`,
    `web=2/3` after `cycle-scale web`.
  - `compose mux down` then `up` → position reset to 1.
  - Static validation: compose file with `mux:` leaf `scale: 4` against
    `commands:` `scale: 2` fails to load with the new error.

Run: `go test ./pkg/cmdman/mux/... ./pkg/cmdman/compose/... ./pkg/muxctl/...`
and `go test ./e2e/cmdman -run 'Mux'` (driver/e2e tests need a real `tmux`).

## Known limitations (documented, not solved)

- The read-positions-then-build gap in `up` is racy against a concurrent
  `cycle-scale`; accepted for an interactive single-user tool.
- A `compose scale` change between `cycle-scale` invocations is handled by
  wrapping, not by erroring — the user may land on an unexpected (but valid)
  replica after scaling down.
- Per-pane/window user options remain tmux-specific (same portability note
  as `@cmdman_marker` / mux-01's driver contract).

## Execution & status tracking

Implementation is dispatched as workstream tasks. Live status lives in
`STATUS.md` next to this plan (`doc/plan/mux-02-scale-cycling/STATUS.md`):
one section per workstream with state (`todo` / `in-progress` / `done` /
`blocked`), a short summary of what was changed, and the exact verification
commands run. Agents update only their own workstream section. Read
STATUS.md first when reviewing or resuming this plan.

Workstreams, in dependency order:

1. **compose-validate** — `Normalize` mux-vs-commands validation + unit
   tests. Independent of everything else.
2. **driver** — `pkg/muxctl` `Leaf.CycleKey`; tmux `@cmdman_leaf`,
   `@cmdman_scale` read/write, `FindLeafPane`, `RespawnLeaf`, `Detach`
   clear + real-tmux tests.
3. **mux-layer** — `Build` decoupling (`BuildOptions`, positions, no
   expansion), `CycleScale`, `ReadScaleState`, unit tests. Depends on 2.
4. **cli** — `compose mux cycle-scale` subcommand (`go-edit-cobra`),
   `runComposeMuxUp` / standalone `mux up` / TUI `CycleMux` call-site
   updates, `pkg/cmdman/cli` rendering. Depends on 3.
5. **docs** — man-page rewrite + sweep. Depends on 4 for final wording.
6. **e2e** — `e2e/cmdman/mux_test.go` scenarios above. Depends on 4.

## Decision log

- **2026-06-12 — interview decisions (user-approved).**
  (a) Fully decouple replica cycling from layout cycling; `up` cycles
  layouts only, the pseudo-layout expansion is removed.
  (b) `cycle-scale` requires exactly one command argument; the explicit
  `<command>=<N>` jump form is in scope. No-arg, multi-arg, `--prev`,
  TUI keybinding, and standalone `mux cycle-scale` are out of scope.
  (c) Validation is static (normalize-time, `mux:` vs `commands:`) plus a
  live clamp at resolution time.
  (d) Pane update is a targeted respawn of affected panes, not a window
  rebuild; other panes keep their state.
  (e) `cycle-scale` with no dashboard window errors (points at
  `compose mux up`); it never builds implicitly.
  (f) Replica positions persist across layout switches; they reset on
  `mux down` / window destruction.
  (g) Plan directory is `mux-02-scale-cycling` (the requested `mux-01` slot
  is taken by `mux-01-fix-fragile-window-detection`).
- **2026-06-12 — `ls` SCALE column pulled into scope (user request).**
  `compose mux ls` shows per-window cycle-target positions and live replica
  counts (`web=2/3`); standalone `mux ls` shows positions only. This makes
  `compose mux ls` open the cmdman store, which `down`/`ls` previously did
  not need — accepted, counts degrade to `?` when unresolvable.

## Out of scope (candidate follow-up)

- TUI keybinding for cycle-scale (the backend plumbing from workstream 4
  makes this a thin addition).
- Backward cycling (`--prev`).
- Standalone `cmdman mux cycle-scale` (needs a ReplicaCounter for plain
  cmdman commands; today standalone mux has none — the same gap keeps
  standalone `mux ls` count-less in the SCALE column).
- No-arg `cycle-scale` advancing every cycle-target at once.
