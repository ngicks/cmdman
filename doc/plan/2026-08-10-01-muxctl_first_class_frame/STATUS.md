# Status

**Current state: draft — scoped from parent plan step 14, questions
unresolved.** (2026-08-10)

This plan directory was created by step 14 of the
[parent plan](../2026-07-26-01-quicklaunch_frame_monitor_state/PLAN.md)
("Scope (and if needed spawn) the muxctl sub-plan"), as D1 mandates and D36
commits to. Requirements are grounded against the current driver code with
`file:line` citations in [PLAN.md](./PLAN.md); nothing is implemented and
no design question is settled.

## Question resolution

- [ ] Q1 API shape — additive sibling vs revised `ApplyLayout`
- [ ] Q2 second identity's home + how enumeration exposes/filters it
- [ ] Q3 which side owns `@cmdman_window`
- [ ] Q4 marker semantics on a framed window
- [ ] Q5 who owns focus policy
- [ ] Q6 main region with no project shown yet
- [ ] Q7 frame pane lifecycle on hide/cycle (managed entries)
- [ ] Q8 teardown when neither side is left
- [ ] Q9 driver-neutral contract vs tmux-scoped
- [ ] Q10 pane-name namespace

Contracts to pin before implementation (PLAN.md "Contracts to pin"):

- [ ] `muxctl.Session` / `muxctl.Server` API delta + the interface docs that
      currently state the violated invariants
- [ ] durable state vocabulary (`@cmdman_frame`, second identity, def name)
- [ ] `muxctl.Window` row shape

## Implementation checklist (mirrors PLAN.md steps)

- [ ] 1. `@cmdman_frame` pane stamp + recognition
- [ ] 2. Subtree-scoped apply (anchored entry point; scoped `resetWindow`)
- [ ] 3. Scoped viewer quiesce
- [ ] 4. Marker semantics / cycling regression
- [ ] 5. Identity coexistence + enumeration
- [ ] 6. Per-side teardown
- [ ] 7. Window-takeover guard
- [ ] 8. Focus policy + contract doc updates

## Next action

Resolve Q1–Q10 with the user, starting with Q1/Q2/Q3 — they decide the API
surface and therefore the size of everything after. Fold each answer into
PLAN.md and append a DECISION.md entry as it resolves.

Blocking relationship: the parent plan's step 15 (frame verbs + switcher
widget) consumes this plan's outcome; parent phases 0–2 and steps 12–14 are
independent of it.
