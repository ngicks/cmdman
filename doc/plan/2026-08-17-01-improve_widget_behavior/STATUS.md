# STATUS — improve TUI widget behavior

**Current state**: implementation in progress (autonomous run started
2026-08-18). Steps 1–6 done. Step-6 notes for later: the launcher keeps a
row's Running marker until the next listing after a mux down (deliberate);
`TestLogs_SinceCrossesRotation` in e2e flaked once under full-suite load,
passed alone and on rerun — pre-existing, unrelated. Note for step 9: doc/man/cmdman-tui.1.md's
launcher opening-list description ("directories you have brought projects
up in") now understates the view — config-dir locations are listed too.

## Checklist (mirrors PLAN.md steps)

- [x] Step 1 — remove `z` + single-caller plumbing + docs (IDEA §4;
      "all plumbing whose only caller was `z` is gone; docs match")
- [x] Step 2 — harden hide per D5: "frame-stamped panes are removed by
      their stamp regardless of recorded state, so panes and state can
      never desync" (incl. the F7-ordering check for managed entries)
- [x] Step 3 — `FromConfig` provenance threaded backend → core types
      (IDEA §1; D2 provenance half)
- [x] Step 4 — empty-filter admission per D2: "appear … ordered after
      history rows … and start **disabled**"; store/cwd sources stay
      filter-only
- [x] Step 5 — `Backend.MuxDown` / `Backend.ComposeDown` + `serviceBackend`
      impls + `FakeBackend` doubles (IDEA §2; D3 backend half, incl. the
      no-`mux:` sentinel error)
- [x] Step 6 — `d`/`D` in switcher, launcher, projectmanager per D3:
      "`D` shows 'compose down <project>? y/n'"; footers, Long helps,
      `doc/man/cmdman-tui.1.md`
- [ ] Step 7 — per D4 as amended 2026-08-18: driver `New` "always
      creates; the by-name adoption … is deleted; WindowName is …
      display-only"; `mux.Run` finds-or-creates cmdman-side via
      `ListWindows` by identity; incl. "unnamed projects get a synthesized
      identity from the workdir hash" (absorbs the switcher_creates_window
      HANDOFF item)
- [ ] Step 8 — `KeepLayout` per D6: "an existing window re-applies the
      layout its marker records; a fresh window gets layout 0"; call sites
      classified bring-up vs cycle — `CycleMux` and CLI keep cycling
- [ ] Step 9 — docs truth sweep + full build/test/e2e + review skills

## Traceability (gate run 2026-08-17)

- D2 "visible on empty filter" → steps 3–4; "start disabled" → step 4;
  "store/cwd stay filter-only" → step 4 (non-goal guard)
- D3 "d / D + confirm" → step 6; "no-mux: error" → steps 5–6
- D4 (amended) "New … always creates; by-name adoption … deleted" → step 7
  driver half; "mux.Run orchestrates find-or-create cmdman-side via
  ListWindows" → step 7 cmdman half; "synthesized identity for unnamed"
  → step 7; "affects every mux up caller" → step 7 tests + risk note
- D5 "state-independent hide" → step 2
- D6 "KeepLayout" → step 8; "cycling stays an explicit gesture" → step 8's
  call-site classification (CycleMux / CLI excluded)
- IDEA §1→steps 3–4, §2→steps 5–6, §3→step 7, §4→steps 1–2, §5→step 8
- HANDOFF.md entry 1 = user-approved deferral, links D7 (broader muxctl
  cleanup revisited later; step 7's targeted fix stays in scope here). The
  inherited item this plan absorbs is cited in step 7 (D4); the inherited
  mux-socket item is carried forward inside HANDOFF entry 1.

## Next action

Begin implementation at step 1 (z removal), or hand the plan to an
implementer. Steps 1–2, 3–6, and 7–8 are largely independent tracks.

## Blocked

Nothing blocked.
