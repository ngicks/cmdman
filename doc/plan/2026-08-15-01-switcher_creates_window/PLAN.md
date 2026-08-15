# Plan — switcher creates the project window

Switcher selection becomes find-or-create-then-jump: selecting a project
creates its multiplexer window when missing and focuses it, instead of the
navigate-only behavior that errored when no window was up.

> Written retroactively: the change was implemented in an autonomous run and
> this plan records what was decided and built. See DECISION.md for the
> `[automatic]` decision entries.

## Goal / success criteria

- Selecting a project in the switcher lands the user in that project's mux
  window whether or not one existed (IDEA.md use case).
- Selecting a project whose window exists focuses it — never a duplicate.
- A project with no derivable identity still gets the hint-line message.
- `go build ./...`, `go test ./...` (incl. e2e), `golangci-lint run` green.

## Non-goals

- Bringing the project up (compose up) on selection.
- Window close/cleanup for accumulated per-project windows.
- Resolving a workdir for identity-less projects via `LoadAndNormalize`.

## Context

- Selection: `cmdman/tui/widget/internal/panel/panel.go` `switchToSelected`
  → `core.SwitchProjectCmd` → `Backend.SwitchToProject`.
- Old impl (`cmdman/cli/tui_backend_mux.go`) pre-checked `mux.List` and
  returned "no window is up for it" when empty; `mux.Land` was used
  focus-only.
- `mux.Land` (`cmdman/mux/land.go`) already implements find-or-create+focus,
  stamping `OwnedIdentity`; the launcher's `S` gesture uses it via
  `compose.Service.MuxLand`.
- Identity: `compose.ProjectSelection{WorkDir, Project}.ProjectIdentity()`,
  stamped onto `core.ProjectInfo.Identity` / `core.ProjectGroup.Identity` by
  `mergeProjectInfos` (`cmdman/cli/tui_backend_compose.go`).

## Approach

Widen the backend call to a `core.SwitchTarget{Identity, WorkDir, Project}`
and have `serviceBackend.SwitchToProject` call `mux.Land` directly with the
group's pre-stamped identity plus a window name derived from
`compose.ProjectSelection.MuxWindowName()`. Rejected alternatives:

- Keep `SwitchToProject(ctx, identity)` and just drop the `mux.List`
  pre-check — rejected: identity alone cannot produce a well-formed window
  (no window name, no workdir).
- Route through `compose.Service.MuxLand` with a re-derived selection —
  rejected: identity must be hashed from the raw workdir spelling while
  `ProjectGroup.Workdir` is symlink-resolved (pinned by
  `TestMergeProjectInfosStampsIdentity`); re-deriving would miss existing
  windows under symlinks. See DECISION.md "Identity key" amendment.

## Public surface delta

No exported-package or CLI-flag surface changes; the delta is the internal
TUI backend contract and user-visible behavior:

```go
// cmdman/tui/internal/core (aliased as tui.SwitchTarget)
type SwitchTarget struct {
        Identity string
        WorkDir  string
        Project  string
}

// Backend — was SwitchToProject(ctx context.Context, identity string) error
SwitchToProject(ctx context.Context, target SwitchTarget) error
```

User-visible: switcher enter/click on a windowless project now creates the
window and jumps instead of reporting "no window is up for it"
(doc/man/cmdman-tui.1.md).

## Implementation steps (all done)

1. `core.SwitchTarget` + interface/doc change (`backend.go`), command
   plumbing (`commands.go`), `tui.SwitchTarget` alias (`alias.go`).
2. Panel builds the target from `g.Identity/g.Workdir/g.Name`; empty-identity
   dead end kept (`panel.go`).
3. Backend impl: `mux.Land` with `WindowName`/`Identity`/`WorkDir`
   (`tui_backend_mux.go`); `mux.List` pre-check removed.
4. Fake backend records `[]core.SwitchTarget` (`coretest.go`); panel tests
   assert the full target triple (`panel_test.go`).
5. New e2e `TestTUIWidget_SwitcherSelectionLandsInProjectWindow`
   (`e2e/cmdman/tui_widget_test.go`): create-then-jump, second selection
   returns to the same window id.
6. Docs: `doc/man/cmdman-tui.1.md` find-or-create prose; switcher subcommand
   `Long` help.

## Testing / verification

Unit: panel tests pin dispatch gating and the dispatched target. E2E: real
tmux window creation, identity stamp equality, find-not-create on re-select.
Full `go test ./...` + `golangci-lint run` green.

## Risks

- A dashboard on a dedicated mux socket is not found (driver autodetect,
  pre-existing) — see HANDOFF.md.
- Per-project windows accumulate; cleanup is the multiplexer's
  disposable-viewer model (non-goal).

## Open questions

None open.
