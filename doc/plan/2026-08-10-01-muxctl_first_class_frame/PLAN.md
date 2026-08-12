# Plan: muxctl first-class frame

Revise the muxctl driver contract so a frame def can be shown, hidden,
selected, and cycled around a project window without disturbing the
project's panes — subtree-scoped apply, `@cmdman_frame` pane stamps,
identity coexistence, and teardown that spares the other side.

Status: **finalized — all ten open questions resolved with the user on
2026-08-12** ([DECISION.md](./DECISION.md) F1–F10). The contracts below are
pinned; implementation may begin at step 1.

**Provenance (in the parent plan's terms).** This is the plan
[D1](../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md) mandates
("B's muxctl contract revision is a separate plan") and
[D36](../2026-07-26-01-quicklaunch_frame_monitor_state/DECISION.md)
commits to ("straight to the first-class frame", no compile-in spike). It
is scoped by step 14 of the
[parent PLAN.md](../2026-07-26-01-quicklaunch_frame_monitor_state/PLAN.md),
and the parent's step 15 (frame verbs + switcher widget) consumes it. The
requirements below are the ones the parent's NOTES.md "The hard part the
original sketch understated: window ownership" enumerates, re-derived here
against the code with citations.

## Goal / success criteria

The tmux driver supports, observably:

1. **Show.** Given a carved frame tree (`frame.Spec.Carve`,
   `pkg/cmdman/frame/carve.go:41`) and a window holding a project layout,
   the frame panes are realized at the edges and the project's panes are
   resized into the remainder — still running, no viewer respawn.
2. **Project operations are subtree-scoped.** `mux up`, layout cycling, and
   cycle-scale rebuild only the project region. Frame panes survive
   untouched and are never sent `ViewerDetachKeys`.
3. **Hide / select / cycle.** Frame panes can be removed or replaced
   without rebuilding the project region.
4. **Teardown is per-side.** Project teardown (`mux down`) collapses the
   project region and leaves the frame; frame teardown removes the frame
   and leaves the project.
5. **Both identities are discoverable.** A framed window reports both which
   frame and which project it holds, enumerable server-wide with no
   attached client, so `mux ls` / `mux down` / the launcher keep working.
6. **Layout cycling still cycles.** `StatWindow`-driven cycling
   (`pkg/cmdman/mux/run.go:151-161`) advances correctly on a framed window.
7. **Focus lands in the project region**, never in a docked frame pane.

## Scope

- `pkg/muxctl` contract (interface docs + any new methods/types) and the
  `pkg/muxctl/tmux` driver.
- The consumer sites that break or must learn the new state:
  `pkg/cmdman/mux/run.go`, `down.go`, `list.go`, `cycle_scale.go`.
- Scripted-tmux e2e proving the lifecycle in the goal criteria.

## Non-goals

- The frame verbs, the switcher widget, and any TUI work — parent plan
  step 15.
- Frame def parsing, discovery, validation, carving — already implemented
  in `pkg/cmdman/frame` (`spec.go`, `discover.go`, `normalize.go`,
  `carve.go`).
- Zellij / wezterm drivers. Window-level user options are tmux-specific and
  already carry that caveat (`pkg/muxctl/tmux/scale_state.go:32-34`); the
  *contract* is stated driver-neutrally with a tmux-only implementation
  (F9), as the ownership stamp already is.
- Any change to what a frame def can express (D15/D16/D19/D30 are settled).

## Context — what the code does today

Every citation below was read in full; nothing here is reconstructed.

### The two contract facts a frame violates

- **One identity slot per window.** `pkg/muxctl/session.go:7` states "A
  single command invocation owns exactly one window". Ownership is the
  single window-level tmux user option `@cmdman_window`
  (`pkg/muxctl/tmux/tmux.go:19`), set once at `Server.New`
  (`tmux.go:110-117`), matched by **exact equality** in `ListWindows`
  (`pkg/muxctl/tmux/list.go:97-100`, with unstamped windows skipped at
  `list.go:92-96`), and cleared by `Detach`
  (`pkg/muxctl/tmux/detach.go:42`). One slot, one value.
- **`ApplyLayout` resets the whole window.** `ApplyLayout` calls
  `resetWindow` before building anything (`pkg/muxctl/tmux/apply.go:26`),
  and `resetWindow` kills every pane but the first
  (`apply.go:158-176`, kill loop at `apply.go:170-173`). `Detach` does the
  same (`detach.go:16`). The interface documents the reset as contract, not
  implementation detail (`session.go:21`).

### Pane-level stamps — the established precedent

`@cmdman_marker` (`apply.go:124`) records the applied layout index, and
`@cmdman_leaf` (`apply.go:127`) records a leaf's cycle key; both are
written by `stampLeaf` (`pkg/muxctl/tmux/leaf.go:49-99`, marker at
`leaf.go:68-81`, leaf key at `leaf.go:83-95`) and read back by
`FindPane` (`leaf.go:107-129`) and `StatWindow`
(`pkg/muxctl/tmux/stat.go:28-31`). Per-window state has its own opaque
mechanism: `muxctl.StateKey` (`pkg/muxctl/driver.go:38-44`) maps to the
window option `@cmdman_<key>` (`scale_state.go:13`, `scale_state.go:19-21`)
and is read/written via `Server.ReadWindowState` / `WriteWindowState`
(`scale_state.go:43-57`, `scale_state.go:64-84`) and fetched inline by
`ListWindows` through `ListOptions.StateKeys` (`driver.go:52-65`,
`list.go:40-43`, `list.go:102-112`).

### The frame package — what step 15 already has

- `frame.Spec.Carve(main muxctl.PaneSpec, component ComponentArgv)
  (muxctl.PaneSpec, error)` (`pkg/cmdman/frame/carve.go:41`) returns a pane
  tree with `main` at the innermost leftover position; entry *i* becomes a
  two-child container `[entry, rest]` (`carve.go:65-84`).
- `frame.EntryPaneName(i)` names the entry leaves `frame-<i>`
  (`carve.go:22-25`) — the only naming hook the driver can key on today.
- `frame.ComponentArgv` (`carve.go:15`) is the seam through which
  `component: switcher` resolves to the widget argv (D37's
  `cmdman tui widget <name>`); the driver never sees component names.
- `frame.LoadAndNormalize` is documented as "the entry point for the frame
  verbs" (`pkg/cmdman/frame/discover.go:135-148`).
- Entry sizes map onto `muxctl.Size` via `Size.MuxSize()`
  (`pkg/cmdman/frame/spec.go:96-98`), so no new size grammar is needed —
  `ComputeChildCells` already resolves percent-of-container to cells
  (`pkg/muxctl/layout.go:8-60`).

**Honesty note on provenance.** The caller's brief cites two follow-ups
from the frame package's implementation (focus policy left to the verbs
step; live-window frame-pane recognition needs a pane stamp). A grep over
`pkg/cmdman/frame/*.go` finds no such notes in the source (only
`discover.go:137` and `carve_test.go:14` mention the verbs), and the
package is untracked, so there is no commit message either. Both follow-ups
are therefore carried from the brief and **independently re-derived from
code** below as R3 and R8 — not attributed to a file.

## Requirements

Each states what must change and cites the code it revises.

### R1 — Subtree-scoped apply

A project layout must be applicable **anchored on a given pane** instead of
resetting the window. Today the only entry point resets:
`ApplyLayout` → `resetWindow` (`apply.go:26`), which kills every pane but
the anchor (`apply.go:158-176`). The build machinery below that line is
already anchor-relative — `materialize(anchorID, root, w, h)`
(`apply.go:90-121`) splits off the passed anchor — so the change is at the
entry point, not in the carve. Decided (F1): **`ApplyLayout` itself is
revised in place** to rebuild only the project region — no additive
sibling in the style of `RespawnLeaf` (`session.go:67-73`,
`leaf.go:138-148`), which was the rejected alternative. On a window with
no frame panes the revised semantics degenerate to today's documented
whole-window reset, so existing callers observe no change.

### R2 — The viewer-quiesce sweep must be scoped too

`ApplyLayout` quiesces before resetting (`apply.go:24`); `quiesceViewers`
(`detach.go:69-93`) sends `cfg.ViewerDetachKeys` to every pane
`listViewerPanes` returns, and that selection is **marker-keyed**: panes
whose `@cmdman_marker` is non-empty (`detach.go:98-122`, filter at
`detach.go:115-118`). So a project apply would send detach keys *into the
frame's widget processes* unless the sweep is scoped to the project
subtree. Scoping `resetWindow` alone is not enough. (`quiesceSinglePane`,
`leaf.go:21-39`, is the single-pane precedent.)

### R3 — `@cmdman_frame` pane stamps

Nothing on a live window distinguishes a frame pane from a project pane
today: the driver knows only `@cmdman_marker` (`apply.go:124`) and
`@cmdman_leaf` (`apply.go:127`), and `frame-<i>` names
(`pkg/cmdman/frame/carve.go:22-25`) exist only in the spec the consumer
holds — pane border titles carry names (`stat.go:22-25`) but titles are
explicitly not durable identity (`pkg/muxctl/doc.go:11-12`). A per-pane
`@cmdman_frame` option, written by the same `stampLeaf` path
(`leaf.go:49-99`) and read like `FindPane` reads `@cmdman_leaf`
(`leaf.go:107-129`), is the same trick at the same layer — it makes "which
panes are the frame" a question the driver can answer about a window it did
not just build.

### R4 — `resetWindow` and `Detach` must spare the other side

- `resetWindow` (`apply.go:158-176`) kills everything but `ids[0]`, so any
  frame pane sharing the window dies on the next apply; it must skip
  frame-stamped panes (R3) and anchor on the project region.
- `Detach` (`detach.go:12-47`) is whole-window teardown: reset
  (`detach.go:16`), clear the anchor's marker/leaf options
  (`detach.go:21-22`), respawn a shell (`detach.go:28`), unset
  `pane-border-status` (`detach.go:36-40`), clear `@cmdman_window`
  (`detach.go:42`) and `@cmdman_scale` (`detach.go:44`). Called per matched
  row by `mux down` (`pkg/cmdman/mux/down.go:132-155`), this wipes the
  frame along with the project. Teardown must become per-side: project
  teardown spares frame panes and frame state, frame teardown spares the
  project's.

### R5 — Identity coexistence and enumeration

With the frame owning `@cmdman_window`, the project identity needs a second
home and every enumeration path must learn it:

- The stamp is written once, from `Config.OwnedIdentity`
  (`pkg/muxctl/driver.go:20-22`, `tmux.go:110-117`), and `Window.Identity`
  is a single field (`driver.go:79-80`).
- `ListWindows` filters `identity != opts.Identity` (`list.go:97-100`) —
  a project-identity query would not match a framed window.
- Consumers depending on that match: `Down` (`down.go:105-108`, then
  `Open` + `Detach` per row, `down.go:132-155`), `List`
  (`pkg/cmdman/mux/list.go:80-84`), and `CycleScale`
  (`pkg/cmdman/mux/cycle_scale.go:94-98`, which errors outright when the
  identity matches nothing, `cycle_scale.go:102-106`). Compose supplies the
  project identity from
  `compose.GenerateProjectIdentity` (`pkg/cmdman/compose/hash.go:54`) via
  `ProjectSelection.ProjectIdentity` (`pkg/cmdman/compose/selection.go:38-43`), and
  the parent plan's launcher (phase 1, step 4) maps windows back to
  projects through exactly this value.

Decided (F2/F3): **the project keeps the owner slot** — `@cmdman_window`
holds the project identity unchanged, so every consumer above keeps
matching — and the frame moves to the second home: a `StateKey`-backed
window option (`driver.go:38-59`; tmux mapping at `scale_state.go:19-21`;
inline fetch at `list.go:40-43`) holding the shown def's name, surfaced as
an explicit second field on `muxctl.Window`, with identity filtering able
to match either slot. Details pinned under **Pinned contracts**.

### R6 — Window takeover must not swallow a framed window

`currentWindowToReuse` returns the caller's current window whenever it
carries **any** non-empty `@cmdman_window` (`pkg/muxctl/tmux/reuse.go:23-61`,
decisive branch at `reuse.go:52-56`), and `Run` enables that takeover
whenever `$TMUX` is set with no explicit `--session`
(`pkg/cmdman/mux/run.go:127` → `tmux.go:77-81`). Under the parent NOTES.md
sketch (frame holds the owner slot) this is acute: a plain `cmdman mux up`
typed inside a *different* project's framed window takes it over, and
`apply.go:26` → `apply.go:158-176` kills the frame. Under F3 (project
holds the slot) the hazard shrinks but does not vanish — the branch still
accepts any non-empty identity, including another project's. Either way
the takeover path must distinguish "my identity" from "someone else's".
(`Open`'s teardown-side counterpart `currentWindowIfOwned`,
`reuse.go:72-85`, accepts any owned window for the same reason and needs
the same distinction.)

### R7 — Marker consistency, or layout cycling silently resets

`StatWindow` treats a pane with no numeric `@cmdman_marker` as breaking
consistency with the marker-bearing panes (`stat.go:50-57`) and returns
`Marker: -1` when panes disagree (`stat.go:68-70`). `Run` computes the next
layout index from exactly that value — `stat.Marker >= 0` advances,
otherwise it falls back to index 0 (`pkg/cmdman/mux/run.go:151-161`). So
unmarked frame panes sharing the window would make every `mux up` snap back
to the first layout — and cycle-scale is worse than silent: it rejects the
window outright when `Window.Marker < 0`
(`pkg/cmdman/mux/cycle_scale.go:145-151`, the value filled per row at
`pkg/muxctl/tmux/list.go:114-122`). Giving frame panes the project's marker
is not a fix either — that is precisely what makes R2's sweep pick them up
(`detach.go:115-118`). A related trap in the same consumer: `FindPane`
scans **every** pane in the window for `@cmdman_leaf`
(`pkg/muxctl/tmux/leaf.go:107-129`, called at `cycle_scale.go:214`), so a
frame pane must never carry a cycle key. Decided (F4): frame-stamped panes
are **excluded from `StatWindow`'s consistency scan** — only project panes
vote on the marker — and never carry `@cmdman_leaf` or `@cmdman_marker`.

### R8 — Focus policy

`ApplyLayout` selects the pane `PickFocus` names (`apply.go:62-69`), and
`PickFocus` falls back to the first leaf in document order when no leaf
sets `Focus` (`pkg/muxctl/layout.go:104-108`). In a carved frame tree a
`top`/`left` entry is the **leading** child (`carve.go:69-80`), so the
first leaf is `frame-0` — the switcher would take focus on every apply.
The levers are `Leaf.Focus` (`pkg/muxctl/spec.go:105-109`), which
`Validate` allows at most once per layout (`validate.go:30-35`), and/or a
driver-side rule that frame-stamped panes are never focus candidates.
Decided (F5): **the driver owns the policy** — frame-stamped panes are
never focus candidates, so no consumer can get it wrong.

### R9 — Pane-name namespace

If the frame tree and the project layout were ever validated as one
`muxctl.Layout`, `Validate` would reject duplicate leaf names across the
whole tree (`validate.go:23-29`), so a project leaf named `frame-0`
(`carve.go:22-25`) would collide. Decided (F10): the two trees are
**always validated and applied as separate calls** and never merged into
one `muxctl.Layout`, so the rule never sees both; no reserved `frame-`
prefix. Also, `Carve` requires `main` to be a well-formed
leaf-or-container (`pkg/muxctl/spec.go:140-142`, `validate.go:45-56`):
there is no expressible "empty main region" today. Decided (F6): when a
frame is shown on a window with no project, the main region holds the
driver's **default pane** — whatever the driver respawns by default, with
no "shell" emphasis in the contract — replaced by the first project apply.

## What the parent plan's step 15 consumes

The frame verbs need these operations. The API delta delivering them is
pinned under **Pinned contracts** (per F1).

| Verb | Driver operations needed | Blocking requirement |
| --- | --- | --- |
| `show` | find-or-create the window; stamp frame identity + def name; apply the carved tree with the project region as anchor; stamp frame panes; keep focus in main | R1, R3, R5, R8 |
| `hide` | locate frame panes on a live window; kill them; leave the project region occupying the whole window | R3, R4 |
| `select` / `cycle` | hide + show in one step, preserving the project subtree; read back the current def name from window state | R3, R4, R5 |
| project `up` / layout cycle / cycle-scale inside a frame | subtree-scoped apply + scoped quiesce + marker that survives | R1, R2, R6, R7 |
| project `down` inside a frame | per-side teardown | R4, R5 |
| `mux ls` / launcher enumeration | both identities on the enumerated row | R5 |

Seams the frame package already exposes, so this plan need not invent them:
`Spec.Carve` (`carve.go:41`) for the tree, `EntryPaneName`
(`carve.go:22-25`) for the entry leaf names, `ComponentArgv`
(`carve.go:15`) for D37's widget argv, `Size.MuxSize`
(`pkg/cmdman/frame/spec.go:96-98`) for sizing, and `LoadAndNormalize`
(`discover.go:135-148`) for def resolution.

## Pinned contracts

The expensive-to-change surfaces, now decided (DECISION.md F1–F10).
Implementation must match these; a change here reopens the corresponding
F-entry.

1. **`muxctl.Session` API delta (F1, F5, F6, F7, F8).** No additive apply
   sibling — `ApplyLayout` is revised in place to be frame-aware:
   - `ApplyLayout` rebuilds only the project region. `resetWindow` spares
     frame-stamped panes, the viewer quiesce excludes them (R2), and they
     are never focus candidates (F5). On a window with no frame panes this
     degenerates to today's whole-window reset, so existing callers observe
     no behavior change.
   - New frame operations beside it:
     - `ShowFrame(ctx context.Context, root muxctl.PaneSpec, mainName
       string) error` — realize a carved frame tree around the window's
       current content: the leaf named `mainName` in `root` stands for the
       existing project region, which is resized, never rebuilt; on a
       window with no project it stands for the driver's default pane
       (F6 — whatever the driver respawns by default, no "shell" emphasis
       in the contract). Frame panes are stamped as they are created.
       *(Routine call noted, not user-asked: designating main by leaf name
       — the consumer passes a placeholder leaf to `frame.Spec.Carve` and
       hands its name to `ShowFrame`; it reuses the existing leaf-name
       vocabulary instead of inventing a sentinel type.)*
     - `HideFrame(ctx context.Context) error` — kill all frame-stamped
       panes so the project region expands to the whole window. All frame
       panes are treated alike (F7): managed-ness (D19) is entirely the
       consumer's concern above the driver. Select/cycle are consumer
       compositions of hide + show.
   - Per-side teardown (F8): project teardown and frame teardown each
     clear only their own side's panes and state; whichever call removes
     the **last** cmdman-owned state performs today's full restore
     (`detach.go:12-47` — respawn default pane, unset
     `pane-border-status`, clear all `@cmdman_*` options).
   - The interface docs that currently state the violated invariants are
     rewritten as part of this plan: `session.go:7` (one window, one
     owner), `session.go:21` (apply resets), `session.go:58-61` (detach
     clears the stamp), `driver.go:167-173` (enumeration by one identity),
     `doc.go:5-12`. The contract is stated driver-neutrally in `muxctl`
     and implemented only in the tmux driver (F9), exactly as the
     ownership stamp is today.
2. **Durable state vocabulary (F2, F3, F4).** Extends the existing
   `@cmdman_` vocabulary (`driver.go:38-44`, `scale_state.go:13-21`,
   `apply.go:124-127`); nothing is duplicated:
   - `@cmdman_window` (window option) — **unchanged**: holds the project
     identity (F3). The frame never occupies the owner slot.
   - `@cmdman_frame` (pane option, new) — marks a pane as a frame pane;
     written through the `stampLeaf` path (`leaf.go:49-99`), read back by
     a `FindPane`-style scan (`leaf.go:107-129`). Frame panes never carry
     `@cmdman_marker` or `@cmdman_leaf`, and `StatWindow`'s marker
     consistency scan skips them (F4) — only project panes vote.
   - Frame def name (window option, new) — the shown def's name in a
     `StateKey`-backed window option (`muxctl.StateKey` → tmux
     `@cmdman_frame_def`); non-empty means "framed". Written by
     `ShowFrame`, cleared by frame teardown.
3. **`muxctl.Window` row shape (F2).** `Window` (`driver.go:67-91`) gains
   an explicit frame field carrying the frame def name, filled by
   `ListWindows` through the existing inline `StateKeys` fetch
   (`list.go:40-43`, `list.go:102-112`); identity filtering can match
   either slot, so "windows of project P" and "framed windows" are both
   answerable. `mux ls` maps the new field into `OwnedWindow`
   (`pkg/cmdman/mux/list.go:93-103`).
4. **Validation (F10).** The frame tree and the project layout are always
   validated and applied as separate calls, never merged into one
   `muxctl.Layout`; `Validate`'s duplicate-name rule (`validate.go:23-29`)
   never sees both trees, and no `frame-` prefix is reserved.

## Implementation steps

Ordered so each lands independently verifiable; decisions folded in
(F-references per step).

1. **Frame pane stamp + recognition (F2).** `@cmdman_frame` written
   through `stampLeaf` (`leaf.go:49-99`), read back by a `FindPane`-style
   scan (`leaf.go:107-129`). Verify: unit test on a scripted tmux server —
   stamp, list, recognize.
2. **Frame-aware `ApplyLayout` (F1).** Revise the entry point in place:
   `resetWindow` (`apply.go:158-176`) spares frame-stamped panes and
   anchors on the project region, over the existing `materialize`
   (`apply.go:90-121`). Verify: apply a layout into a window that already
   has frame panes — frame pane ids unchanged; the existing apply suites
   pass unchanged, proving the unframed degenerate case.
3. **Scoped quiesce (R2).** `listViewerPanes` (`detach.go:98-122`) must
   exclude frame panes. Verify: assert no `send-keys` reaches a frame pane
   during a project apply.
4. **Marker semantics (F4).** Revise `StatWindow` (`stat.go:28-71`) so
   frame-stamped panes are skipped by the consistency scan. Verify:
   regression on the cycling path (`run.go:151-161`) — three `mux up`s on
   a framed window visit three layouts.
5. **`ShowFrame` / `HideFrame` (F1, F6, F7).** Realize a carved tree
   around the existing region (or the driver's default pane when there is
   none), stamping frame panes and writing `@cmdman_frame_def`; hide kills
   all frame-stamped panes uniformly and the project region expands.
   Verify: show around a running project — project pane ids unchanged;
   hide — project region fills the window, project panes still running.
6. **Identity coexistence + enumeration (F2/F3).** Frame def name in its
   `StateKey`-backed home; `ListWindows` (`list.go:25-134`) filling the
   new `Window` field (`driver.go:67-91`). Verify: `mux ls` and a
   project-identity `Down` query both find a framed window.
7. **Per-side teardown (F8).** Split `Detach` (`detach.go:12-47`) into
   project teardown and frame teardown, with the full restore firing only
   on the last side. Verify: `mux down` leaves the frame; frame hide
   leaves the project; tearing down the last side leaves a clean window
   with no residual `@cmdman_*` state.
8. **Takeover guard (R6).** `currentWindowToReuse` (`reuse.go:23-61`) /
   `currentWindowIfOwned` (`reuse.go:72-85`) distinguishing identities.
   Verify: `mux up` from inside a framed window does not eat the frame.
9. **Focus policy (F5) + contract doc updates (F9).** Frame-stamped panes
   excluded from focus selection around `PickFocus` (`apply.go:62-69`,
   `layout.go:78-108`); `session.go`, `driver.go`, `doc.go` rewritten to
   state the new invariants driver-neutrally, since today they state the
   violated ones.

## Testing / verification

- Unit tests beside the driver, against a scratch tmux server on a temp
  socket, as the package already does — existing suites to extend:
  `pkg/muxctl/tmux/ownership_test.go`, `detach_test.go`,
  `apply_order_test.go`, `cycle_scale_test.go`, with helpers in
  `helpers_test.go` (files present; contents not read for this scoping).
- The regression that matters most is R7's: cycling on a framed window.
- e2e in `e2e/cmdman` for the parent's step-15 lifecycle once the verbs
  exist — out of scope here, named so it is not forgotten.

## Risks

- **The interface docs are load-bearing.** `session.go:7` / `:21` and
  `doc.go:5-12` are cited by the parent plan as the reason this work is
  separate; a revision that leaves them stale is worse than no revision.
- **Two identities double the teardown surface.** `Detach` already clears
  four pieces of state (`detach.go:21-22`, `:36-40`, `:42`, `:44`);
  per-side teardown must not leave a half-stamped window that neither side
  recognizes.
- **tmux-specific mechanism.** Window/pane user options have no zellij or
  wezterm equivalent — the caveat is already recorded at
  `scale_state.go:32-34`. F9 contains this: the contract is stated
  neutrally in `muxctl`, and only the tmux driver implements it.
- **The parent plan blocks from its step 15 on** (parent PLAN.md step 14,
  Risks). Phases 0–2 there are independent, so the block is contained, but
  it is real.

## Open questions

*(none — all ten resolved with the user on 2026-08-12; the choices,
rationale, and rejected alternatives are recorded in
[DECISION.md](./DECISION.md) as F1–F10, mapping Q1→F1 … Q10→F10. The only
answer that departed from the drafted tentative default was Q1/F1: revise
`ApplyLayout` in place rather than adding an additive sibling.)*
