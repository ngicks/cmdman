# TUI_MUX — implementation state

Status: **DONE**

## What landed

`pkg/cmdman/tui/mux.go`:

- `cycleMux` — the Compose-tab `c` handler. No-mux projects report a status
  message. In **popup mode** the cycle runs immediately (the TUI is in a popup,
  so rearranging the underlying window is safe). In **direct mode** it first
  opens a warning popup (`popupMuxWarn`, defaults to `<cancel>`) because the
  layout would rearrange the window holding the TUI.
- `cycleMuxCmd` / `onMuxDone` — async invocation + status reporting. The TUI
  keeps no layout state; mux owns its persisted tmux window marker.
- `popupMuxWarn` popup kind with the documented warning body and `<continue>`
  action button.

`Options.PopupMode` threads through: `cli.RunTUI` → false, `cli.RunTUIChild` →
true.

`pkg/cmdman/cli/tui_backend.go`:

- `CycleMux` — mirrors `cmdman compose mux`: `compose.LoadOrProject` →
  `selection.Spec.Mux` → `mux.Build` (with `ResolveCommandID` resolver) →
  `mux.Run` (stdout discarded). Errors (incl. zellij "not implemented" from
  `mux.Run`) surface in the footer.
- `projectHasMux` — sets the Compose-tab `mux` badge by loading each project's
  compose file and checking `Spec.Mux != nil` (the badge is not in
  `ProjectSummary`). Wired into `ListProjects` enrichment.

`l` reports cycle-only ("Specific layout selection is not available yet; use c
to cycle layouts.") since `mux.Run` only auto-cycles.

## Tests

`mux_test.go`: mux badge rendered, no-section status, popup-mode immediate
invoke, direct-mode warns-first + confirm dispatches, cancel does not cycle,
mux-done status (success/error), `l` cycle-only. zellij not-implemented is
covered on the popup side by `cli/tui_test.go` (`resolvePopupDriver`) and on the
mux side by `mux.Run`'s existing guard.

## Deferred / notes

- **Pane-count guard** ("reject if the current window already has multiple
  panes") is intentionally NOT implemented: there is no production pane-count
  API (the only `#{window_panes}` usage is an internal dev tool). The non-popup
  warning is the v1 safety. When a pane-count API lands, add the rejection as a
  complementary check in `cycleMux`.
- Direct-mode cycle, once confirmed, still rearranges the TUI's own window (as
  warned); popup mode is the recommended path.
- zellij popup + mux remain not-implemented stubs (v1 is tmux-only).
