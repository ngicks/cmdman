# cmdman tui plan

## Goal

Add `cmdman tui` as an interactive terminal UI for managing compose-managed
commands.

The first version covers command lifecycle operations for commands defined by
cmdman compose projects:

- list commands
- start commands
- stop commands
- restart commands
- attach commands
- remove commands

Standalone, non-compose commands are out of scope for v1. The TUI does not
create commands through `cmdman run` or `cmdman create`, and it does not edit
compose files. Those flows can be added later as explicit features.

This plan is intentionally limited to the initial shape of the TUI. More flows can
be added after the command browser, filtering, preview, and navigation model are
settled.

## Focused Plans

This file is the overview. More detailed design should go into:

- [TUI_CORE.md](./TUI_CORE.md): tabs, lists, filtering, selection, popups, and
  command lifecycle keys
- [TUI_RUNTIME.md](./TUI_RUNTIME.md): events API subscription,
  preview refresh, async actions, and attach terminal handoff
- [TUI_MUX.md](./TUI_MUX.md): mux layout cycling and compose
  mux integration

## Primary Screen

The TUI is multi-tab. The default tab is the current command list view.

Initial tabs:

- `Commands`: default side-by-side command browser
- `Compose`: compose projects found in the default compose directory

The `Commands` tab is a side-by-side browser:

- top area: fuzzy finder input
- tab bar above the filter input
- left pane: selectable command menu grouped under compose projects
- right pane: preview of the selected command output
- bottom area: key hints, status/error messages, and a version line at the right edge

```text
+------------------------------------------------------------------------------+
| cmdman tui                                                                    |
| [Commands]  Compose                                                           |
| Filter: _                                                                     |
+--------------------------------------+---------------------------------------+
| Commands                             | Preview                               |
|                                      |                                       |
| v ⿻ local-dev             active     | $ cmdman compose logs watcher        |
|   > watcher          running         |                                       |
|     seed-db          exited          | 2026-05-30T09:00:00Z watching files  |
|                                      | 2026-05-30T09:00:01Z ready           |
| v ⿻ api-stack                        |                                       |
|   > web              running         |                                       |
|     worker           exited          |                                       |
|     migrate          exited(0)       |                                       |
| > ⿻ tools                            |                                       |
|                                      |                                       |
+--------------------------------------+---------------------------------------+
| tab next  j/k move  h/l fold  / filter  s start  S stop  r restart  a attach |
| q quit                                                         v0.0.7-devel |
+------------------------------------------------------------------------------+
```

The `Compose` tab lists compose projects found under the default directory.

```text
+------------------------------------------------------------------------------+
| cmdman tui                                                                    |
|  Commands  [Compose]                                                          |
| Filter: _                                                                     |
+------------------------------------------------------------------------------+
| Compose projects in ~/.config/cmdman/compose                                  |
|                                                                              |
| > ⿻ local-dev        active           2 commands        mux                   |
|   ⿻ api-stack                         3 commands        mux                   |
|   ⿻ tools                             0 commands        modified 2026-05-20   |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+
| tab next  j/k move  / filter  enter open  c cycle mux  r refresh             |
| q quit                                                         v0.0.7-devel |
+------------------------------------------------------------------------------+
```

## Layout Details

### Tabs

The TUI has a top tab bar under the title.

Initial tabs:

- `Commands`: default tab, showing the command tree and preview pane
- `Compose`: lists compose projects from the default compose directory

Tab behavior:

- `tab` moves to the next tab
- `shift-tab` moves to the previous tab if the terminal input library supports it
- each tab keeps its own selection, filter text, and scroll position
- switching tabs should not reset command fold state
- the bottom key hint line changes per active tab

### Fuzzy Finder

The filter input lives at the top of the screen and limits the command list as the
user types. Filtering applies to the active tab.

In the `Commands` tab, filtering should match:

- compose project name
- command name
- command status or display label

When filtering is active, matching commands remain visible under their compose
project. A project group may be shown even if the project name matches and only
some child commands match.

In the `Compose` tab, filtering should match:

- compose project name
- compose project path
- project metadata shown in the list

### Command Tree

Commands are gathered under compose projects. Each compose project is a foldable
element.

Projects tied to the current working directory are shown at the top before other
projects. These rows should carry an `active` marker so users can distinguish
the current workspace from other configured compose projects.

```text
v ⿻ local-dev        active
  > watcher          running
    seed-db          exited
v ⿻ api-stack
  > web              running
    worker           exited
    migrate          exited(0)
> ⿻ tools
```

Fold state:

- `v` means open
- `>` means folded

Selection state:

- selected command rows use `>` in the command row prefix
- selected project rows may also be supported for folding only
- lifecycle actions operate on the selected command, not the project

If project-level actions are added later, they should be explicit and not implied
by selecting the group row.

### Compose Project List

The `Compose` tab lists compose projects in the default compose directory.

Initial behavior:

- show one row per discovered compose project
- show projects tied to the current working directory at the top by comparing
  `ProjectSummary.WorkDir` to `os.Getwd()`
- use `⿻` as the app/project marker
- show project name, active marker, command count, and a compact metadata column
- `enter` opens the selected project in the `Commands` tab by applying a project
  filter or moving selection to that project
- `c` invokes compose mux layout cycling for the selected project when it has a
  `mux:`
  section
- `l` is future work unless a "show layout N" mux entry point is added; v1 may
  show a status message explaining that only cycle is supported
- `r` refreshes the project list
- empty state is shown when no compose projects exist in the default directory

Example empty state:

```text
No compose projects found in ~/.config/cmdman/compose.
```

### Compose Mux Layouts

The `Compose` tab provides a thin wrapper over the existing compose mux command.
The TUI does not track a selected layout per project. Layout cycling state stays
owned by mux's persisted tmux window marker.

Mux behavior:

- `c` invokes the existing compose mux path for the selected project and lets mux
  cycle to the next layout
- projects without a `mux:` section show a status message
- mux display is safest through `cmdman tui --popup=tmux`; without popup mode,
  the TUI must warn before invoking mux because mux rearranges the current
  window
- zellij floating-pane support is a TODO stub for v1

### Preview Pane

The preview pane shows output for the selected command.

Initial behavior:

- show recent output for the selected command
- update when selection changes
- keep the command list usable while output is loading
- show an empty state when the selected command has no output
- distinguish "no output yet" from "no log storage configured" for commands that
  use the `none` log driver
- show a clear error when logs cannot be read

Example empty state:

```text
No output yet.
```

Example no-storage state:

```text
No log storage configured for this command.
```

### Status Labels

Persisted command states come from `pkg/cmdman/model/state.go`. The TUI may use
friendlier display labels, but all logic should use the real state values.

| Persisted state | Display label |
| --- | --- |
| `created` | `created` |
| `starting` | `starting` |
| `started` | `running` |
| `exited` | `exited` or `exited(<code>)` when exit code is available |
| `failed` | `failed` |

There is no persisted `stopped` state. A stopped process eventually appears as
`exited` or `failed`.

Example error state:

```text
Unable to read command output:
permission denied
```

## Navigation

Movement is vim-like.

Initial key map:

```text
tab            move to next tab
shift-tab      move to previous tab, if supported
j / down       move selection down
k / up         move selection up
h / left       fold selected project, or move focus to command list
l / right      open selected project, or move focus to preview
/              focus filter input
esc            leave filter input or cancel pending action
enter          open selected item, when the active tab defines an open action
s              start selected command
S              stop selected command
a              attach selected command after confirmation
r              restart selected command
x              remove selected command after confirmation
?              show help
q              quit the TUI
```

The active tab can add tab-specific meanings. In the `Compose` tab, `enter`
opens the selected compose project in the `Commands` tab, and `r` refreshes the
project list. `c` cycles the selected project's mux layout when available, and
`l` is future work unless mux gains a "show layout N" entry point.

`?` opens a read-only help overlay for the active tab. It lists navigation,
filter, lifecycle, compose mux, and popup confirmation keys. `esc` or `?`
closes help without triggering actions; `q` quits the TUI.

While the filter input has focus, character keys edit the filter and single-key
bindings such as `s`, `S`, `r`, `a`, `x`, `c`, `q`, and `?` are inert. `esc`
leaves filter focus first.

Open questions for later:

- whether `enter` should open an action menu on command rows
- whether preview scrolling should use focus plus `j/k`, or separate keys like
  `ctrl-d` and `ctrl-u`
- whether project rows should be selectable or only foldable

## Lifecycle Actions

Actions should be available from the selected command row.

### Attach

Attach to the selected command after confirmation.

Attach should temporarily hand terminal ownership from the TUI to the command
attach session. When the user detaches, control returns to the TUI menu at the
previous selection.

Expected TUI behavior:

- `a` opens an attach confirmation popup
- selection moves between the popup's visible action button and `<cancel>`
- default selection is `<yes>`
- `enter` confirms the selected popup action
- TUI rendering is suspended before attach starts
- terminal state is restored for the attach session
- detach returns to the TUI menu and redraws the active tab
- command exit returns to the TUI menu with a status message
- attach errors are reported in the bottom status area after the TUI redraws

Confirmation layout:

```text
+------------------------------------------------------------------------------+
| Attach to command?                                                            |
|                                                                              |
| project: api-stack                                                            |
| command: web                                                                  |
|                                                                              |
|                         <yes>        <cancel>                                 |
+------------------------------------------------------------------------------+
```

Implementation note:

The existing attach primitive supports this flow. The TUI should open a session
with `Service.OpenAttachSession` and call `cli.Attach` directly so detach keys
return to the TUI. Do not route through the default Cobra `cmdman attach` flow,
because that command uses sticky attach behavior unless `--auto-exit` is set.

### Start

Start a command that is not currently `started` or `starting`.

Expected TUI behavior:

- action runs asynchronously
- selected row shows a pending state while the request is in flight
- status refreshes after completion
- errors are reported in the bottom status area

### Stop

Stop a running command.

Expected TUI behavior:

- action runs asynchronously
- selected row shows a pending state while the request is in flight
- status refreshes after completion
- errors are reported in the bottom status area

### Restart

Restart the selected command.

Expected TUI behavior:

- action runs asynchronously
- command status should not appear stale while restarting
- preview should continue to show available output and then refresh

### Remove

Remove the selected command after confirmation.

Expected TUI behavior:

- selection moves between the popup's visible action button and `<cancel>`
- `enter` confirms the selected popup action

Confirmation layout:

```text
+------------------------------------------------------------------------------+
| Remove command?                                                               |
|                                                                              |
| project: api-stack                                                            |
| command: web                                                                  |
|                                                                              |
|                         <yes>        <cancel>                                 |
+------------------------------------------------------------------------------+
```

If the selected command is running, the confirmation must make force removal
explicit because removing a running command requires `--force` / SIGKILL.

Force confirmation layout:

```text
+------------------------------------------------------------------------------+
| Force remove running command?                                                 |
|                                                                              |
| project: api-stack                                                            |
| command: web                                                                  |
|                                                                              |
| This sends SIGKILL before removing the command.                                |
|                                                                              |
|                         <force remove>        <cancel>                        |
+------------------------------------------------------------------------------+
```

## State Model

The TUI should keep view state separate from command state.

View state:

- current filter text
- selected row
- active tab
- tab-local filter text
- focused pane
- fold state per compose project
- preview scroll position
- compose project list scroll position
- pending confirmation dialog
- pending action per command
- attach-in-progress state while the TUI is suspended

Command state:

- compose project
- command name
- current status
- command metadata needed by start/stop/restart/remove
- recent output preview

Compose project state:

- project name
- project path in the default compose directory
- command count
- last modified time or other compact metadata
- whether the project is tied to the current working directory
- whether the project has a `mux:` section

## List Data Sources

The TUI is compose-scoped. List data must be loaded from compose-aware sources
instead of treating the global event log as the source of truth.

Commands tab:

- seed and refresh command rows with `cmdman.Service.List` using
  `ListRequest{AllStates:true}` filtered to compose commands by labels
- require the compose labels `compose.LabelWorkdir` and `compose.LabelProject`;
  rows without those labels are standalone commands and stay out of v1 scope
- group command rows client-side by the `compose.LabelWorkdir` and
  `compose.LabelProject` values
- discover never-run projects in the default compose directory with
  `compose.ListNamedProjects()` and merge them into the tree as project rows
  with zero commands
- do not scan the current working directory for compose files in v1

Compose tab:

- start from `compose.Service.ListProjects()` for store-known project counts
  (`Commands`, `Running`, `Exited`, `Failed`)
- merge in names from `compose.ListNamedProjects()` so default-location projects
  that have never been run still appear with `0 commands`
- determine the active project by comparing the workdir label from
  `Service.List` results to `os.Getwd()`
- do not use cwd compose-file discovery or `cmdman compose config` verification
  for active-project detection in v1
- the `mux` badge is not available from `Service.List` or `ListProjects`; render
  it only after opening the project's compose file and finding `Spec.Mux != nil`

## Refresh Model

The TUI should subscribe to `cmdman.Service.Events` and reflect local event-log
changes to the screen. `Service.Events` is a local JSONL file tail, not a
network stream, and there is no central daemon to reconnect to. The event log is
global and includes standalone commands, so events are change signals rather
than list rows.

Expected behavior:

- subscribe to `cmdman.Service.Events` while the TUI is running
- debounce lifecycle events and re-run the list loaders instead of applying
  per-event deltas directly
- filter reloaded rows to compose scope by labels so standalone `cmdman run`
  commands do not appear in the TUI
- use lifecycle events as a signal to refresh command state and reload preview
  content when needed; events do not carry command output
- periodically refresh the compose project list while the `Compose` tab is active
- preserve active-project ordering when compose project state refreshes
- lifecycle actions trigger an immediate refresh when they finish
- current selection is preserved when possible
- fold state is preserved across refreshes
- local event-tail errors are reported in the bottom status area without closing
  the TUI

If the selected command disappears after refresh, selection should move to the
nearest visible command row.

Service calls may share one `*cmdman.Service` instance across event
subscription, preview loading, and async actions. View-state mutations should
still be serialized through the TUI update loop; goroutines send results back as
messages.

The preview pane is loaded through `Service.Logs`, not through the event
subscription. Use `Tail:N` for the initial snapshot and `Follow` only for the
currently selected command. Log readers must run in goroutines and deliver lines
back as Bubble Tea messages so navigation never blocks on log I/O. Keep one live
follow reader at a time and cancel it when selection changes.

## Implementation Notes

Add `cmdman tui` as a thin Cobra command that delegates to a package outside
`cmd/`.

`cmdman tui` must support `--popup[=tmux|zellij]` as a bool-style optional-value
flag. V1 popup support is tmux-only; zellij is a TODO stub and should return a
clear "not implemented" error when selected. When `--popup` is present without a
value, use the same kind of driver inference as mux already uses. This flag
controls where the TUI view appears; it does not enable or disable compose mux
support inside the TUI.

Popup mode launches the TUI as a separate tmux child process. Add a
hidden `cmdman tui __child --ipc <endpoint>` subcommand for the actual popup TUI
process. The public `cmdman tui --popup` process is a launcher that creates a
Unix socket IPC endpoint, opens the popup, waits for startup/final status, and
exits with the child result. Do not use stdout/stderr for launcher control
messages while the popup process owns terminal rendering. See `TUI_CORE.md` for
the popup launcher IPC principle.

The launcher must forward the caller's working directory and cmdman storage
locations to the child process. For tmux, open the popup with
`display-popup -d <cwd>`; zellij already inherits cwd when it is implemented, and
future wezterm support should use `wezterm cli spawn --cwd <cwd> -- ...`. Also
forward `--data-dir`, `--runtime-dir`, and `$CMDMAN_CONF` so popup mode uses the
same active-project calculation and store/runtime targets as direct mode.

Suggested package boundary:

- `cmd/cmdman`: Cobra wiring only
- `pkg/cmdman/cli`: CLI-facing command composition
- `pkg/cmdman/tui`: TUI model, update loop, rendering, and key handling
- existing usecase/store/log packages: command lifecycle and output data

Mux actions should stay a thin wrapper around existing compose mux behavior. The
TUI should not track selected mux layouts. If the TUI is not running in popup
mode, warn before invoking mux because mux rearranges the current tmux window.
When production pane-count detection exists, also reject mux display if the
current window is already split into multiple panes.

The TUI package should be testable without a real terminal by testing model
updates, filtering, fold behavior, and action dispatch separately from rendering.

## Initial Test Coverage

Focus tests on behavior that can regress without a real terminal:

- fuzzy filter matches project name, command name, and status/display label
- filtering keeps commands grouped under projects
- fold state hides and reveals command rows
- tab switching preserves tab-local selection and filter state
- active projects tied to the current working directory sort above other projects
- command list loads compose-scoped rows from `Service.List`
- default-dir projects from `compose.ListNamedProjects()` appear with zero
  commands
- standalone command rows are filtered out by compose-label scope
- lifecycle events trigger a debounced re-list instead of direct row mutation
- compose project list loads projects from the default directory
- compose mux cycle invokes existing compose mux behavior
- `cmdman tui --popup` infers a popup driver when `--popup` has no value
- `cmdman tui --popup=zellij` reports not implemented for v1
- compose mux layout display warns when the TUI is not running in popup mode
- compose mux layout display rejects when the current window has multiple panes,
  once pane-count detection exists
- selection moves only across visible rows
- selection is preserved across refresh when the command still exists
- character keys edit the filter and do not trigger actions while filter focus
  is active
- preview uses `Service.Logs` without blocking navigation
- preview cancels the previous live follow reader on selection change
- attach confirmation requires explicit confirmation
- attach confirmation defaults to `<yes>`
- detach from attach returns to the previous TUI selection
- remove confirmation requires explicit confirmation
- running-command remove shows the force confirmation
- `?` help lists active-tab key bindings
- `none` log driver renders the no-storage preview state
- lifecycle action errors are surfaced in view state
