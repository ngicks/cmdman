# STATUS — TUI big improvements

**State:** planning complete — the 8 original open questions (OQ1–OQ8) are
resolved, plus follow-on design decisions taken during refinement. DECISION.md
holds 11 entries: **D0** (workstream split), **D1–D8** (the OQ resolutions),
**D9** (vt no-remote-resize), **D10** (tabs single source of truth), **D11**
(where the `--tab` helpers live). No open questions remain. Ready to start at
workstream A.

## Checklist (mirrors PLAN.md steps)

- [ ] A1 — canonical `tabs.go` table (`Tab`/`tabDefs`/`TabNames`/`TabKeys`/`ParseTab`/`NumTabs`) + `TabLayout` + `Options.InitialTab`
- [ ] A2 — `--tab` cobra flag (inline usage from `TabKeys`, `ParseTab`, completion) + forward to popup child
- [ ] E1 — popup geometry flags + `PopupConfig` fields + `display-popup` argv
- [ ] B1 — `popupComposeUp` + `Backend.ComposeUp` + confirm flow
- [ ] B2 — `e` editor handoff (`$VISUAL`/`$EDITOR`/vim) + path resolution
- [ ] B3 — `enter` definition viewer overlay + `Backend.ProjectDefinition`
- [ ] C0 — thread `Tty` through projection (`CommandInfo.Tty`/`commandRow.tty`) + tests
- [ ] C1 — add `charmbracelet/x/vt`; raw read-only preview stream contract
- [ ] C2 — vt emulator preview renderer + local resize; predicate + sanitize fallback
- [ ] D1 — `Backend.ListLayouts` + `layoutTab` state/render + default-to-marker
- [ ] D2 — `enter` applies layout (`Backend.ApplyLayout`) + direct-mode warning
- [ ] Docs/tests — help text, e2e, README/architecture note

## Done
- Repo grounding: read the whole TUI package, the popup launcher, the mux
  list/spec/cycle code, compose up, and confirmed `charmbracelet/x/vt` is not yet
  a dependency (sibling of the already-vendored `charmbracelet/x/*`).

## In progress
- None — plan finalized, awaiting go-ahead to implement.

## Blocked / waiting
- Nothing blocking. (R1 risk for vt is retired by the attach-source decision D7.)

## Next action
- Begin **A1** (canonical `tabs.go` table + `TabLayout` + `Options.InitialTab`),
  then **A2** (`--tab` flag). Implement in the recommended order
  A → E → B → C → D, one verifiable PR per step.
