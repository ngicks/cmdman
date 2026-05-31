# TUI core plan

## Scope

This plan covers the normal interactive application shell for `cmdman tui`.

It owns:

- `cmdman tui --popup[=tmux|zellij]` view placement, with zellij as a v1 TODO
  stub
- tabs
- command list layout
- compose project list layout
- filtering
- selection and fold state
- confirmation popups
- command lifecycle key bindings
- footer/status/version rendering

Runtime subscriptions, attach terminal handoff, and compose mux integration are
covered by the sibling runtime and mux plans.

## Library And Process Model

Use `github.com/charmbracelet/bubbletea` as the renderer for `cmdman tui`.
`lipgloss` is already a direct dependency and can be used for styling.

`pkg/cmdman/cli/progress_tty.go` deliberately avoids a full TUI framework
because framework startup can query the terminal for the whole binary and corrupt
the PTY of sibling subcommands such as `compose attach`. That concern does not
apply to `cmdman tui`: it is a standalone interactive process, and it does not
spawn sibling subcommands that share the same PTY. Attach is handled through an
explicit terminal handoff described in `TUI_RUNTIME.md`.

## View Placement

`cmdman tui` can run as the full terminal view or as a multiplexer popup.

Popup mode is a TUI view concern, not a compose mux concern.

The command must support a bool-style optional-value popup flag:

```text
cmdman tui --popup
cmdman tui --popup=tmux
cmdman tui --popup=zellij
```

Behavior:

- `--popup=tmux` opens the TUI view in a tmux popup
- `--popup=zellij` is accepted only after zellij popup support is implemented;
  zellij is a TODO stub for v1
- bare `--popup` infers the popup backend using mux-style environment inference
- v1 popup support is tmux-only; inference that selects zellij should return a
  clear "not implemented" error
- popup mode affects where the TUI runs; it does not enable or disable mux
  support inside the TUI

### Popup Launcher IPC

Popup mode runs the TUI in a separate tmux process. The original
`cmdman tui --popup` process becomes a launcher. The launcher and popup child
process communicate over a small IPC channel for completion and error reporting.
The child must inherit the launcher's effective execution context: working
directory, data directory, runtime directory, and config file.

The reason is the same class of problem solved by tools like `fzf`: once the UI
is running in another terminal context, stdout/stderr are part of the user's UI
surface and are not a reliable control channel back to the original caller. A
child process also cannot directly mutate parent-process state such as shell
state or exit status. The launcher needs an explicit channel to know whether the
popup TUI started, exited normally, failed, or was closed by the multiplexer.

Principles:

- avoid IPC when the TUI runs directly in the current terminal process
- use IPC only for popup mode if launcher and TUI are separate processes
- keep the IPC payload small: startup status, final exit status, and final error
  message are enough for the first version
- do not send normal rendered UI through the IPC channel
- do not depend on stdout/stderr for launcher control messages while the popup
  owns terminal rendering
- clean up Unix socket paths on both normal exit and failure
- create IPC endpoints with user-only permissions
- make launcher waits cancellable so a failed popup start cannot hang forever

Use a Unix domain socket for popup launcher IPC. cmdman already uses Unix-domain
IPC elsewhere, so keeping the popup launcher on the same transport family avoids
adding named-pipe-specific behavior.

### Hidden Child Command

Add a hidden internal subcommand for popup execution:

```text
cmdman tui __child --ipc <endpoint>
```

Behavior:

- `cmdman tui --popup` is the public launcher entry point
- the launcher infers or selects the popup backend
- the launcher creates a Unix socket IPC endpoint with user-only permissions
- the launcher asks tmux to open a popup running `cmdman tui __child`
- the launcher passes its working directory to tmux with `display-popup -d <cwd>`
- the launcher forwards `--data-dir`, `--runtime-dir`, and `$CMDMAN_CONF` to the
  child so it uses the same store, runtime directory, and config file as the
  parent
- `__child` runs the actual TUI inside the popup terminal context
- `__child` reports startup success or failure over IPC
- `__child` reports final exit status and final error message over IPC
- the launcher waits for IPC completion and exits with the child result
- the launcher cleans up the IPC endpoint

`__child` is not a stable user-facing command. It should be hidden from help and
completion output.

Flow:

```text
cmdman tui --popup
  -> infer tmux when --popup has no value
  -> create IPC endpoint
  -> capture cwd, data dir, runtime dir, and config file
  -> run multiplexer popup command:
       tmux display-popup -d <cwd> 'cmdman tui __child --ipc <endpoint> ...'
  -> wait for child startup/final result
  -> clean up IPC
  -> exit with child status

cmdman tui __child --ipc <endpoint>
  -> connect/report to IPC endpoint
  -> run TUI in popup terminal
  -> report final status
```

Non-popup direct mode does not need this propagation because there is no child
process. Zellij is a future stub and inherits cwd by default; future wezterm
support should use `wezterm cli spawn --cwd <cwd> -- ...`.

## Screens

### Commands Tab

The `Commands` tab is the default tab. It shows commands grouped under compose
projects, with a preview pane on the right.

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

Project rows use `⿻` as the app/project marker.

Projects tied to the current working directory are listed before other projects
and show an `active` marker.

### Compose Tab

The `Compose` tab lists compose projects discovered from the default compose
directory.

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
+------------------------------------------------------------------------------+
| tab next  j/k move  / filter  enter open  c cycle mux  r refresh             |
| q quit                                                         v0.0.7-devel |
+------------------------------------------------------------------------------+
```

## State

View state:

- active tab
- selected row per tab
- filter text per tab
- scroll position per tab
- focused pane
- fold state per compose project
- pending confirmation popup
- pending command action
- status message

Command row state:

- compose project
- command name
- command id
- current status
- active/pending marker

Compose project row state:

- project name
- project path
- command count
- active current-working-directory marker
- compact metadata
- mux summary when a `mux:` section exists

Do not keep selected mux layout state in the TUI. Mux layout cycling state is
owned by the existing mux command and its persisted tmux window marker.

## Filtering

Filtering applies to the active tab.

In the `Commands` tab, the filter matches:

- compose project name
- command name
- command status or display label

In the `Compose` tab, the filter matches:

- compose project name
- compose project path
- visible metadata

Filtered command results stay grouped under compose projects. If a project name
matches, the project row is visible even when only some child commands match.

While the filter input has focus, character keys edit the filter. Single-key TUI
bindings such as `s`, `S`, `r`, `a`, `x`, `c`, `q`, and `?` are inert until the
filter loses focus. `esc` leaves filter focus first.

## Navigation

Base key map:

```text
tab            next tab
shift-tab      previous tab, when supported
j / down       move selection down
k / up         move selection up
h / left       fold selected project, or move focus left
l / right      open selected project, or move focus right
/              focus filter input
esc            leave filter input or cancel popup
enter          activate selected item or popup choice
?              show help
q              quit the TUI
```

Command actions:

```text
s              start selected command
S              stop selected command
r              restart selected command
a              attach selected command
x              remove selected command
```

Compose tab actions:

```text
enter          open selected project in Commands tab
c              cycle selected project's mux layout
l              layout selector, future work unless mux supports "show layout N"
r              refresh compose project list
```

`q` quits the normal TUI screen. This does not change attach handoff behavior:
`Ctrl+c` during attach is forwarded to the remote command.

## Help Screen

`?` opens an in-TUI help overlay for the active tab. It should list the same
bindings shown in the footer plus less common bindings that do not fit there.

Behavior:

- `?` opens help
- `esc` or `?` closes help and returns to the previous selection
- `q` quits the TUI, even while help is open
- help content changes with the active tab
- help is read-only and does not trigger lifecycle actions

Initial help sections:

- navigation keys
- filter keys
- command lifecycle keys on the `Commands` tab
- compose mux keys on the `Compose` tab
- popup confirmation keys

## Confirmation Popups

Destructive or terminal-handoff actions use a selection popup. They do not use
`y` or `n` hotkeys.

Attach popup:

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

Remove popup:

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

Remove confirmation for a running command must make the force behavior explicit:

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

Popup behavior:

- selection moves between the popup's visible action button and `<cancel>`
- `enter` confirms the selected choice
- `esc` cancels
- the default selection should be `<yes>` for attach
- the default selection should be `<cancel>` for remove

## Lifecycle Actions

State-changing commands are explicit; there is no start/stop toggle.

- `s` starts a command that is not currently `started` or `starting`
- `S` stops a running command
- `r` restarts the selected command
- `x` opens remove confirmation

V1 limitations:

- stop and restart use the service defaults for signal and timeout
- bulk actions are not supported; actions target only the selected command
- running-command remove requires an explicit force confirmation

Actions run asynchronously. While an action is in flight, the selected row should
show a pending marker and further duplicate actions for the same command should
be ignored or reported as already pending.

## Test Coverage

Core tests should cover:

- tab switching preserves tab-local state
- filtering matches command and compose project fields
- command rows remain grouped after filtering
- active current-working-directory projects sort above inactive projects
- status labels map from the real persisted state vocabulary
- running-command remove shows the force confirmation
- `?` opens help with the active tab's key bindings
- filter focus makes character keys edit the filter instead of triggering
  lifecycle/quit bindings
- fold state hides and reveals command rows
- selection moves only across visible rows
- selection is preserved across refresh when possible
- lifecycle actions are explicit and do not toggle on `enter`
- attach and remove confirmations require selecting a popup choice
- attach defaults to yes
- remove defaults to cancel
