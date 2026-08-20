# Status — switcher command row restyle + wheel scroll

State: **planned, finalized** — gate reconfirmed by user 2026-08-20 after the
`M`-binding addition (R9). All questions resolved (DECISION.md R1–R9). The
"dashboard target on top" sort idea was retracted by the user (see R9's
note) — deliberately not planned. Branch fast-forwarded onto main's cb1f875
(Layout-tab removal; Commands tab intact, step 5 unaffected) and PLAN.md line
refs re-checked against it. Ready to implement pending the user's go-ahead.

## Checklist

- [x] IDEA.md gate reconfirmed after R9 — `confirmed by user, 2026-08-20`
- [x] Open questions resolved (R1–R9, DECISION.md)
- [x] PLAN.md detailed; traceability table maps every R-clause to a step
- [x] Step 1 — core helpers: `ClampCells`/`ScaleCell` (R8, R4)/`RowNameStyle`
      (R1 "Strong red, flat yellow, idle/done -> flat green, unreported ->
      weak green", R3)/`RowPayload` (R2, D13); badge deletion deferred to
      step 5 so every per-step commit builds (see DECISION.md [automatic])
- [x] Step 2 — switcher row anatomy (R5 bell column, R6 separator, R8 nameW)
- [x] Step 3 — switcher wheel scroll (R7: "wheel scrolls the view; the next
      keyboard move snaps the view back")
- [x] Step 4 — switcher `M` binding (R9: "open manager for currently
      displayed tmux window's dashboard target")
- [x] Step 5 — Commands tab same language (R4 "both surfaces"); the three
      old badges deleted here with the last caller migration
- [ ] Step 6 — e2e expectation fixes, full build/test/lint, review skills
- [x] e2e wheel coverage: confirmed by inspection of the vendored decoder —
      the harness can inject wheel events via
      `tmux send-keys -l $'\x1b[<64;x;yM'`; ultraviolet parses SGR mouse
      unconditionally (`decoder.go:412`), so no gap — the wheel e2e lands in
      step 6

## Next action

User gives the implementation go-ahead; on approval, implement steps 1–6 in
order.
