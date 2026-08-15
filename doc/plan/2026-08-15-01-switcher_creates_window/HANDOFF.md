# Handoff — switcher creates the project window

## Identity-less projects still dead-end (deferral)

- **What**: a switcher group with `Identity == ""` (no resolved workdir or
  project name) dispatches nothing and shows the hint-line message
  (`cmdman/tui/widget/internal/panel/panel.go`, `switchToSelected`).
  Resolving a workdir the way the launcher's `loadLaunchSpec` does
  (`compose.LoadAndNormalize` in `cmdman/cli/tui_backend_launcher.go`) would
  let those projects get a window too.
- **Why not here**: `[automatic]` deferral — DECISION.md "Projects with no
  derivable identity stay a dead end". Decided without the user; confirm or
  pick up.
- **Follow-up**: if wanted, a small plan to resolve a `ProjectSelection` for
  identity-less groups in `serviceBackend.SwitchToProject`.

## Dedicated mux socket windows are not found (out-of-scope discovery)

- **What**: `serviceBackend.SwitchToProject` (`cmdman/cli/tui_backend_mux.go`)
  passes no `Driver` to `mux.Land`, so the driver autodetects; a project
  window living on a dedicated mux socket is missed and a duplicate is
  created on the default socket. Pre-existing — the old navigate-only
  implementation passed no driver either.
- **Follow-up**: resolving the compose file to learn the project's mux/driver
  config; belongs with any future driver-plumbing work for the TUI backend.

## `os.IsNotExist` lint trap in e2e (out-of-scope discovery)

- **What**: `e2e/cmdman/compose_test.go:1595` uses `os.IsNotExist`, which the
  `ngcheckers` PostToolUse hook rejects on any edit under `e2e/cmdman/`.
  Pre-existing; blocks unrelated future edits in that package.
- **Follow-up**: switch to `errors.Is(err, fs.ErrNotExist)` in a cleanup
  commit.
