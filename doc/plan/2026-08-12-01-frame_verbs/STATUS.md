# Status

**Current state: finalized — ready for implementation.** (2026-08-13)

Idea gate passed (IDEA.md confirmed as written), all nine questions
resolved (DECISION.md V1–V9), traceability gate passed below.

Standalone plan delivering the quicklaunch parent plan's step 15 (the
parent predates sub-plan management and is not restructured — user's
call). Everything below the verbs exists: `cmdman/frame` (parent steps
11–12), the widgets (step 13), the driver contract
([muxctl plan](../2026-08-10-01-muxctl_first_class_frame/STATUS.md),
implemented 2026-08-12), and the `default_frame` config key (added
2026-08-12 after review caught it unowned).

## Question resolution

All resolved 2026-08-13.

- [x] Q9 frame presence across window switches → V9 (`mux up`
      auto-shows `default_frame`)
- [x] Q1 verb mount → V1 (`cmdman mux frame …`)
- [x] Q2 `select` verb → V2 (folded into `show DEF`)
- [x] Q3 cycle order & start → V3 (sorted `ListNamedDefs`; routine
      call, noted)
- [x] Q4 `frame ls` → V4 (ships now)
- [x] Q5 D17 hook-override owner → V5 (owned here, step 5)
- [x] Q6 switcher interaction set → V6 (navigate-only + `--no-quit`;
      no `q` binding when docked — user's refinement)
- [x] Q7 managed entry identity → V7 (`frame-<def>-<i>` + adopt)
- [x] Q8 in-widget collapse gesture → V8 (`z` runs hide)

## Implementation checklist (mirrors PLAN.md steps)

- [ ] 1. Def resolution + FrameShow/FrameHide service layer
- [ ] 2. `mux up` auto-show of `default_frame` (V9)
- [ ] 3. Cycle composition (V3)
- [ ] 4. Managed entry lifecycle (D19/F7/V7)
- [ ] 5. Frame def `hooks:` field (D17/V5)
- [ ] 6. CLI wiring incl. `ls` + man pages (V1/V2/V4)
- [ ] 7. Switcher selection actions + `--no-quit` (D6/D22/V6)
- [ ] 8. Collapse gesture `z` (D16/V8)
- [ ] 9. Lifecycle e2e (parent step-15 verify)

## Traceability gate — passed 2026-08-13

Every operative clause, inherited and local, mapped to an owning step:

- D15 "via config or a command flag" → steps 1, 2
- D15 "shown / hidden / selected / cycled" → steps 1, 3, 6 ("selected"
  = `show DEF` replace, V2)
- D16 "collapse gesture" → step 8 (V8)
- D17 "override the default hooks" → step 5 (V5)
- D19/F7 managed viewer detach-before-kill → step 4 (V7)
- D6 switcher navigation → step 7
- D37 component → widget entrypoint → done upstream (parent step 13);
  step 7 extends the widget, not the resolution
- V1 verb mount / V4 `ls` → step 6
- V3 cycle order → step 3
- V6 `--no-quit` + navigate-only boundary → step 7
- V9 auto-show on `mux up` → step 2
- Parent step-15 verify line → step 9

IDEA.md use cases replayed against the steps: (1) put up the frame →
steps 1, 2, 6; (2) take it down → steps 1, 6; (3) walk the defs →
steps 3, 6; (4) frame first, projects later → driver F6 (done) +
step 9 verifies; (5) switching from the switcher → steps 7, 8;
(6) managed entries → steps 4, 5; (7) knowing what's available/up →
step 6 (`ls`). No unowned use case.

## Next action

Implement step 1 (def resolution + FrameShow/FrameHide in
`cmdman/mux`).
