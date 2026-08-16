# Issues

Known issues awaiting their own plan/commit. Moved out of
`2026-08-15-01-project_manager_widget/HANDOFF.md` (2026-08-17); each entry
keeps its original discovery date.

## `compose up --mux` collides on window index in a shared session (out-of-scope discovery, 2026-08-16)

Bringing a *second* project up with `--mux` into a session that already
holds one fails: `tmux new-window -d -t <session> …: create window failed:
index 1 in use` — `-t <session>` resolves to the session's current window's
*index*, so the insert collides once the current window is not the last
(find-or-create path under `pkg/muxctl/tmux/`). Found while building the
project-manager plan's step-6 summon e2e (it forced the test's project B to
be `create`d instead of brought up). **Follow-up**: fix window creation to
append (`-a`) or pick an explicit free index; deserves its own small
plan/commit.

## Summon workdir is symlink-resolved; symlinked `work_dir:` still misses (out-of-scope discovery, 2026-08-16)

D20's summon passes `ProjectInfo.Workdir`, which is
`normalizePath`/`EvalSymlinks`-resolved (`cmdman/cli/tui_backend.go:82-96`),
while compose identity deliberately preserves symlinks
(`cmdman/compose/normalize.go:118-119`). Right whenever the stored label is
symlink-free (the normal case — labels come from `os.Getwd()` or an explicit
`--workdir`); a project whose `work_dir:` goes through a symlink would still
resolve wrong. **Follow-up**: carry a raw-workdir field through
`ProjectInfo`/`ProjectGroup` (design change; pre-existing hazard also noted
at `cmdman/cli/tui_backend_compose.go:70-76`).

## Full TUI Compose-tab Active mark is still cwd-only (out-of-scope discovery, 2026-08-16)

`cmdman/tui/state.go:371` marks the Compose tab's active project by cwd
match only — it is not among D3's enumerated consumers (switcher Active
mark, `resolveLayoutSelection`, project-manager), so the project-manager
plan's step 3 deliberately did not convert it. **Follow-up**: fold it onto
`ActiveIdentity` in a later change for full D3 consistency.

**User note (2026-08-17, undecided)**: leaning toward dropping the TUI
panel from the `tui` subcommand in favor of new widgets — which would make
this fix moot along with the panel it fixes. No decision yet; whichever
plan picks this issue up should settle that direction first.
