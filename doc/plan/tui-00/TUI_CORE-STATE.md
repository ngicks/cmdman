# TUI_CORE — implementation state

Status: **DONE** (initial core shell landed)

## What landed

Package `pkg/cmdman/tui` (bubbletea renderer) + `pkg/cmdman/cli/tui.go` (launcher
composition) + `cmd/cmdman/commands/tui.go` (cobra wiring).

- `tui.go` — package doc (why the progress_tty PTY concern does not apply),
  `Backend` interface, `CommandInfo`/`ProjectInfo` DTOs, `Options`, `Run`, `New`.
- `state.go` — `Model`, tabs, `commandsTab`/`composeTab`, project groups +
  command rows, compose rows, fold map, visible-row flattening, selection
  movement across visible rows only, active-project sorting (cwd-tied first),
  `displayLabel` state→label mapping.
- `filter.go` — case-insensitive subsequence filter; command match (name +
  state + display label); compose-row match (name + path + workdir + metadata).
- `popup.go` — attach/remove/force-remove confirmation popups (attach defaults
  `<yes>`, remove/force default `<cancel>`), popup + help rendering.
- `update.go` — `Init`/`Update`, list-loaded handlers, action-done handler
  (clears pending, refreshes), selection-by-id preservation, pending markers.
- `keys.go` — modal key routing: popup → help → filter-focus → normal; tab
  switching; filter-focus modality (single keys inert while filtering); h/l
  fold-or-focus; enter folds project rows / no-op on command rows; lifecycle
  keys (s/S/r/a/x); compose-tab keys (enter open, r refresh, c/l placeholders).
- `commands.go` — tea.Cmd builders + messages for load/actions.
- `view.go` — title, tab bar, filter line, two-pane Commands body, Compose list,
  footer hints + right-edge version, centered modal overlay, preview states
  (empty / no-storage / error / loading / ok).
- `backend.go` — `serviceBackend` over `*cmdman.Service` + `*compose.Service`:
  compose-scoped `ListCommands` (requires LabelProject+LabelWorkdir, drops
  standalone), `ListProjects` (ListProjects ∪ ListNamedProjects by name),
  lifecycle actions, normalized cwd/workdir for active detection.
- `cli/tui.go` — `RunTUI` (direct), `RunTUIChild` (popup child + IPC status),
  `LaunchTUIPopup`/`RunTUIPopup` (tmux `display-popup -d <cwd>`, Unix-socket IPC,
  forwards --data-dir/--runtime-dir/$CMDMAN_CONF), driver inference, zellij
  not-implemented, shell quoting.
- `cmd/.../tui.go` — `cmdman tui` (`--popup` BoolFunc) + hidden `tui __child`.

## Tests

`tui_test.go` + `cli/tui_test.go` cover: active sort, display labels, filter
match + grouping, fold hide/reveal, selection clamps to visible, selection
preserved across refresh, tab-local state preserved, filter-focus inertness,
enter no-toggle / fold-toggle, attach default yes, remove default cancel,
running→force confirm, explicit-confirmation remove dispatch, start/stop guards,
action-done clears pending + refreshes, help lists tab bindings, compose filter,
none-driver preview state, driver inference (tmux/fallback/zellij), child argv,
shell quoting.

## Deferred to later phases (by design)

- **RUNTIME**: initial-load wiring is present but events subscription + debounced
  re-list, preview via `Service.Logs` (Tail snapshot + single Follow reader),
  pending-marker-during-flight refresh nuances, and the **attach terminal
  handoff** (currently the attach popup confirm is a placeholder status) are
  TUI_RUNTIME work.
- **MUX**: compose `mux:` discovery (`Spec.Mux`), `c` cycle invocation, non-popup
  warning, pane-count guard, zellij stub are TUI_MUX work. `ProjectInfo.HasMux`
  is currently always false; the `c`/`l` keys are placeholders.
