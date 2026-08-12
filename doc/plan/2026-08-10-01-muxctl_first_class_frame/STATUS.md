# Status

**Current state: finalized — all ten open questions resolved; ready to
implement.** (2026-08-12)

This plan directory was created by step 14 of the
[parent plan](../2026-07-26-01-quicklaunch_frame_monitor_state/PLAN.md)
("Scope (and if needed spawn) the muxctl sub-plan"), as D1 mandates and D36
commits to. Requirements are grounded against the driver code with
`file:line` citations in [PLAN.md](./PLAN.md); contracts are pinned there
under "Pinned contracts"; every design question is settled in
[DECISION.md](./DECISION.md) F1–F10. Nothing is implemented yet.

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

- [ ] 1. `@cmdman_frame` pane stamp + recognition (F2)
- [ ] 2. Frame-aware `ApplyLayout` (F1)
- [ ] 3. Scoped viewer quiesce (R2)
- [ ] 4. Marker semantics / cycling regression (F4)
- [ ] 5. `ShowFrame` / `HideFrame` (F1, F6, F7)
- [ ] 6. Identity coexistence + enumeration (F2/F3)
- [ ] 7. Per-side teardown (F8)
- [ ] 8. Window-takeover guard (R6)
- [ ] 9. Focus policy (F5) + contract doc updates (F9)

## Next action

Begin implementation at step 1 (`@cmdman_frame` pane stamp + recognition),
with a scripted-tmux unit test beside the driver.

Blocking relationship: the parent plan's step 15 (frame verbs + switcher
widget) consumes this plan's outcome; parent phases 0–2 and steps 12–14 are
independent of it.
