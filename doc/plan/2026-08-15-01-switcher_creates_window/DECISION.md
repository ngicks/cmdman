# Decisions — switcher creates the project window

Entries tagged `[automatic]` were decided during an autonomous run without
user input; review and veto freely.

## What "dashboard window for the project" means [automatic]

The window the switcher creates/jumps to is the project's mux window — the
same window the launcher's `S` gesture lands on via
`compose.Service.MuxLand` → `mux.Land` (find-or-create by project identity,
then focus). No new window kind is introduced; the switcher simply gains the
create-if-missing half that `mux.Land` already implements, instead of the
navigate-only `mux.List` pre-check that errored when no window was up.
Rejected: a new window kind running a per-project dashboard TUI — nothing in
the codebase has that notion, and the launcher precedent already defines
"the project's window".

## Identity key for "window already exists" [automatic]

`compose.ProjectSelection{WorkDir, Project}.ProjectIdentity()` — the same
`(WorkDir, Project)`-derived identity already stamped on windows by
`mux.Land` (`OwnedIdentity`) and carried by `core.ProjectGroup.Identity`.
The backend receives the group's `Workdir`/`Name` so it can build the
selection (and thus the window name) instead of receiving only the opaque
identity hash, which is not enough to create a well-formed window.

Amendment [automatic]: the backend passes the group's already-stamped
`Identity` straight to `mux.Land` rather than re-deriving it via
`compose.Service.MuxLand`. `mergeProjectInfos` hashes the identity from the
raw workdir spelling but ships `Workdir` symlink-resolved
(`TestMergeProjectInfosStampsIdentity` pins this), so re-deriving from the
resolved path would miss existing windows and create duplicates under
symlinks. The `ProjectSelection` is still built — for the window name only.

## Projects with no derivable identity stay a dead end [automatic]

A group with `Identity == ""` (no resolved workdir or no project name) still
gets the hint-line message and no command, as today. Rejected: resolving a
workdir via `compose.LoadAndNormalize` (as the launcher's `loadLaunchSpec`
does) — a scope expansion; deferred, see HANDOFF.md.

## Switcher does not bring the project up [automatic]

Unlike the launcher path (`bringUp` + `MuxLand`), the switcher only
creates/focuses the window. The task asks for window creation + jump only;
`mux.Land` synthesizes a bare shell window for a mux-less project (D9
precedent), so no compose up is needed. Rejected: mirroring the launcher's
full bring-up — selection would start commands as a side effect of
navigation.

## No window cleanup / close logic [automatic]

Per-project windows can accumulate, but close/cleanup is out of scope for
this change; left to the multiplexer's disposable-viewer model.
