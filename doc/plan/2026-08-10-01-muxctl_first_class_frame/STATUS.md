# Status

**Current state: implemented — all nine steps landed, reviewed, and
green.** (2026-08-12)

This plan directory was created by step 14 of the
[parent plan](../2026-07-26-01-quicklaunch_frame_monitor_state/PLAN.md)
("Scope (and if needed spawn) the muxctl sub-plan"), as D1 mandates and D36
commits to. Requirements are grounded against the driver code with
`file:line` citations in [PLAN.md](./PLAN.md); contracts are pinned there
under "Pinned contracts"; every design question is settled in
[DECISION.md](./DECISION.md) F1–F10.

## Question resolution

All resolved with the user 2026-08-12. Only Q1 departed from the drafted
tentative default (revise `ApplyLayout` in place, not an additive sibling).

- [x] Q1 API shape → F1 (revise `ApplyLayout`; `ShowFrame`/`HideFrame` new)
- [x] Q2 second identity's home + enumeration → F2 (`StateKey`-backed
      `@cmdman_frame_def`; explicit field on `muxctl.Window`)
- [x] Q3 which side owns `@cmdman_window` → F3 (project keeps it)
- [x] Q4 marker semantics on a framed window → F4 (frame panes excluded)
- [x] Q5 who owns focus policy → F5 (driver-side rule)
- [x] Q6 main region with no project yet → F6 (driver's default pane)
- [x] Q7 frame pane lifecycle on hide/cycle → F7 (driver treats all alike)
- [x] Q8 teardown when neither side is left → F8 (full restore on last side)
- [x] Q9 driver-neutral vs tmux-scoped → F9 (neutral contract, tmux impl)
- [x] Q10 pane-name namespace → F10 (separate validation)

Contracts pinned (PLAN.md "Pinned contracts"):

- [x] `muxctl.Session` API delta + the interface docs to rewrite
- [x] durable state vocabulary (`@cmdman_frame`, `@cmdman_frame_def`,
      `@cmdman_window` unchanged)
- [x] `muxctl.Window` row shape (explicit frame field)

## Implementation checklist (mirrors PLAN.md steps)

All implemented 2026-08-12, each with scripted-tmux unit tests beside the
driver whose failure modes were verified non-vacuous (fix disabled →
test fails on its intended assertion).

- [x] 1. `@cmdman_frame` pane stamp + recognition (F2)
- [x] 2. Frame-aware `ApplyLayout` (F1)
- [x] 3. Scoped viewer quiesce (R2)
- [x] 4. Marker semantics / cycling regression (F4)
- [x] 5. `ShowFrame` / `HideFrame` (F1, F6, F7)
- [x] 6. Identity coexistence + enumeration (F2/F3)
- [x] 7. Per-side teardown (F8)
- [x] 8. Window-takeover guard (R6)
- [x] 9. Focus policy (F5) + contract doc updates (F9)

Verification: a five-focus review pass (bugs / conventions / docs /
history / tests) against F1–F10 found two blocking issues — an
interrupted `ShowFrame` stranding unstamped panes, and the frame-only
window (main region exited) being a dead end for `ApplyLayout`/
`ShowFrame` — both fixed with regression tests, alongside its minor
findings. A second, independent read-only codex (GPT) review against
the pinned contracts then flagged the two deliberate contract
divergences (now recorded as amendments under DECISION.md F1/F2), the
conservative `hasCmdmanState` release rule (kept — see follow-ups), and
stale `down` man-page wording (fixed). Final state: `go build ./...`,
`go test -count=1 ./...` (including `e2e/cmdman`), and
`golangci-lint run ./...` all green.

## Routine calls made during implementation (noted, not user-asked)

- `ShowFrame` gained a fourth parameter: `ShowFrame(ctx, root PaneSpec,
  mainName, defName string)` — F2 pins that `ShowFrame` writes the shown
  def's name into `@cmdman_frame_def`, and the pinned three-arg signature
  had nowhere to carry it. `defName` (and `mainName`) empty is an error.
- "Identity filtering can match either slot" is realized as the
  enumeration *gate* accepting either slot (a window with only a frame
  def still enumerates), while `ListOptions.Identity` matches the
  ownership slot exactly — a destructive `Down --identity dev` can never
  match a window merely framed "dev".
- Takeover guard: `New` accepts a frame-only current window
  (show-before-launch, F6) and declines any foreign-identity owner;
  `Open` declines frame-only windows — the frame side addresses windows
  through `ListWindows`' frame slot, not a current-window guess.
- On teardown of a framed window whose project panes all exited,
  `Detach` skips the collapse instead of erroring (previously stranded
  `@cmdman_window`); `resetWindow`/`ShowFrame` spawn a default main
  region on a frame-only window instead of erroring.
- `mux ls` gained a FRAME table column (repo precedent: SCALE's rollout
  in `50c9bba`), and `.Frame` is documented for `--format` in both man
  pages.

## Known follow-ups (deferred, with reason)

- With `pane-border-status top`, a 1-row frame entry realizes at height
  0 (`split-window -l N` yields N−1 usable rows). Pre-existing for
  project layouts too; will bite the first statusbar def — parent step
  15 should size bars ≥ 2 or address the off-by-one.
- Double `ShowFrame` without an intervening hide stacks panes (leaves no
  orphan state); the frame verbs (parent step 15) should hide-then-show.
- `Open` accepting a frame-only current window is deferred until the
  frame verbs want current-window teardown.
- e2e for the full frame lifecycle belongs to the parent's step 15 once
  the verbs exist (PLAN.md Testing already names this).
- `hasCmdmanState` scans the `@cmdman_` window-option namespace by
  prefix, so a `StateKey` slot added later and cleared by neither side
  would keep the window held (full restore deferred) rather than be
  wiped. Deliberate: releasing a window that still holds unknown cmdman
  state is the worse failure; any new slot must be cleared by the side
  that owns it.

## Next action

None here — this plan is complete. The parent plan's step 15 (frame
verbs + switcher widget) is unblocked and consumes `ShowFrame` /
`HideFrame` / `Window.Frame`.
