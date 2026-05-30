# cmdman tui plan

## Goal

Add `cmdman tui` as an interactive terminal UI for managing commands.

The first version should cover the same core command lifecycle operations that are
available from the CLI:

- list commands
- start commands
- stop commands
- restart commands
- remove commands

This plan is intentionally limited to the initial shape of the TUI. More flows can
be added after the command browser, filtering, preview, and navigation model are
settled.

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
| v ⿻ api-stack                        | $ cmdman logs api-stack/web          |
|   > web              running         |                                       |
|     worker           stopped         | 2026-05-30T09:00:00Z starting web... |
|     migrate          exited(0)       | 2026-05-30T09:00:01Z listening :8080 |
|                                      | 2026-05-30T09:00:05Z GET /health 200 |
| > ⿻ tools                            |                                       |
|                                      |                                       |
| v ⿻ local-dev                        |                                       |
|     watcher          running         |                                       |
|     seed-db          stopped         |                                       |
|                                      |                                       |
+--------------------------------------+---------------------------------------+
| tab next  j/k move  h/l fold  / filter  enter start/stop  r restart  x remove |
| Ctrl+c or Ctrl+d to exit                                                v0.1.0 |
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
| > ⿻ api-stack                         3 commands        modified 2026-05-30   |
|   ⿻ local-dev                         2 commands        modified 2026-05-29   |
|   ⿻ tools                             0 commands        modified 2026-05-20   |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+
| tab next  j/k move  / filter  enter open commands  r refresh                  |
| Ctrl+c or Ctrl+d to exit                                                v0.1.0 |
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
- command status

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

```text
v ⿻ api-stack
  > web              running
    worker           stopped
    migrate          exited(0)
> ⿻ tools
v ⿻ local-dev
    watcher          running
    seed-db          stopped
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
- use `⿻` as the app/project marker
- show project name, command count, and a compact metadata column
- `enter` opens the selected project in the `Commands` tab by applying a project
  filter or moving selection to that project
- `r` refreshes the project list
- empty state is shown when no compose projects exist in the default directory

Example empty state:

```text
No compose projects found in ~/.config/cmdman/compose.
```

### Preview Pane

The preview pane shows output for the selected command.

Initial behavior:

- show recent output for the selected command
- update when selection changes
- keep the command list usable while output is loading
- show an empty state when the selected command has no output
- show a clear error when logs cannot be read

Example empty state:

```text
No output yet.
```

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
enter          start stopped command, stop running command
r              restart selected command
x              remove selected command after confirmation
q              quit
?              show help
```

The active tab can add tab-specific meanings. In the `Compose` tab, `enter`
opens the selected compose project in the `Commands` tab, and `r` refreshes the
project list.

Open questions for later:

- whether `enter` should always open an action menu instead of toggling
- whether preview scrolling should use focus plus `j/k`, or separate keys like
  `ctrl-d` and `ctrl-u`
- whether project rows should be selectable or only foldable

## Lifecycle Actions

Actions should be available from the selected command row.

### Start

Start a stopped or exited command.

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

Confirmation layout:

```text
+------------------------------------------------------------------------------+
| Remove command?                                                               |
|                                                                              |
| project: api-stack                                                            |
| command: web                                                                  |
|                                                                              |
| y confirm    n cancel                                                         |
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

## Refresh Model

The first version can use periodic refresh plus direct refresh after actions.

Expected behavior:

- command list refreshes periodically
- compose project list refreshes periodically while the `Compose` tab is active
- selected command preview refreshes periodically
- lifecycle actions trigger an immediate refresh when they finish
- current selection is preserved when possible
- fold state is preserved across refreshes

If the selected command disappears after refresh, selection should move to the
nearest visible command row.

## Implementation Notes

Add `cmdman tui` as a thin Cobra command that delegates to a package outside
`cmd/`.

Suggested package boundary:

- `cmd/cmdman`: Cobra wiring only
- `pkg/cmdman/cli`: CLI-facing command composition
- `pkg/cmdman/tui`: TUI model, update loop, rendering, and key handling
- existing usecase/store/log packages: command lifecycle and output data

The TUI package should be testable without a real terminal by testing model
updates, filtering, fold behavior, and action dispatch separately from rendering.

## Initial Test Coverage

Focus tests on behavior that can regress without a real terminal:

- fuzzy filter matches project name, command name, and status
- filtering keeps commands grouped under projects
- fold state hides and reveals command rows
- tab switching preserves tab-local selection and filter state
- compose project list loads projects from the default directory
- selection moves only across visible rows
- selection is preserved across refresh when the command still exists
- remove confirmation requires explicit confirmation
- lifecycle action errors are surfaced in view state
