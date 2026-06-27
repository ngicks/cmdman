# STATUS — TUI big improvements

**State:** implementation complete — all five feature workstreams (A–E) have
landed and the final Docs/tests step is done. DECISION.md holds 11 entries:
**D0** (workstream split), **D1–D8** (the OQ resolutions), **D9** (vt
no-remote-resize), **D10** (tabs single source of truth), **D11** (where the
`--tab` helpers live). No open questions remain.

## Checklist (mirrors PLAN.md steps)

- [x] A1 — canonical `tabs.go` table (`Tab`/`tabDefs`/`TabNames`/`TabKeys`/`ParseTab`/`NumTabs`) + `TabLayout` + `Options.InitialTab`
- [x] A2 — `--tab` cobra flag (inline usage from `TabKeys`, `ParseTab`, completion) + forward to popup child
- [x] E1 — popup geometry flags + `PopupConfig` fields + `display-popup` argv
- [x] B1 — `popupComposeUp` + `Backend.ComposeUp` + confirm flow
- [x] B2 — `e` editor handoff (`$VISUAL`/`$EDITOR`/vim) + path resolution
- [x] B3 — `enter` definition viewer overlay + `Backend.ProjectDefinition`
- [x] C0 — thread `Tty` through projection (`CommandInfo.Tty`/`commandRow.tty`) + tests
- [x] C1 — add `charmbracelet/x/vt`; raw read-only preview stream contract
- [x] C2 — vt emulator preview renderer + local resize; predicate + sanitize fallback
- [x] D1 — `Backend.ListLayouts` + `layoutTab` state/render + default-to-marker
- [x] D2 — `enter` applies layout (`Backend.ApplyLayout`) + direct-mode warning
- [x] Docs/tests — help text, e2e, README/architecture note

## Done
- Repo grounding: read the whole TUI package, the popup launcher, the mux
  list/spec/cycle code, compose up, and confirmed `charmbracelet/x/vt` is not yet
  a dependency (sibling of the already-vendored `charmbracelet/x/*`).

## In progress
- None — all steps (A1–D2 + Docs/tests) are implemented.

## Blocked / waiting
- Nothing blocking. (R1 risk for vt is retired by the attach-source decision D7.)

## Next action
- None — implementation complete. The TUI ships three tabs (Commands/Compose/
  Layout), the `--tab` flag, and the `--popup-width/-height/-x/-y` geometry flags;
  README/architecture note and e2e coverage are refreshed.
