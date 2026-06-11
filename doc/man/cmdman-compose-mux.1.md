# cmdman-compose-mux(1)

## Name

`cmdman compose mux` - open, tear down, or list tmux dashboards for a compose project

## Synopsis

```text
cmdman compose [selection flags] mux [--session NAME] [LAYOUT]
cmdman compose [selection flags] mux up [--session NAME] [LAYOUT]
cmdman compose [selection flags] mux down [--session NAME]
cmdman compose [selection flags] mux ls [--session NAME] [--format FORMAT]
```

## Description

Reads the selected compose file's top-level `mux:` section. Each leaf names a
compose service and resolves to the stored cmdman command for the selected
project. Default leaves run sticky attach viewers; `mode: logs` leaves run
sticky log viewers. Mux never moves command ownership into tmux, so closing the
window does not stop the services.

The embedded `mux:` section uses the same layout format documented in
[cmdman-mux(5)](./cmdman-mux.5.md), except that `command` values name compose
services in the selected project.

Without `LAYOUT`, successive invocations cycle through declared layouts. A
layout can be selected by name or zero-based index; names take precedence over
numeric interpretation. The selected index becomes the next cycle position.

Inside tmux, the current session is selected by default. Outside tmux, the
session defaults to `cmdman` and an attach hint is printed. The owned window is
named `cmdman-PROJECT`.

When no compose file is explicitly selected, `compose mux` selects a compose
associated with the current directory that declares a mux section. Ambiguous
selection is an error. Unlike other compose commands, a missing mux section is
always an error.

The root `compose mux` command is an alias of `compose mux up`. A layout
literally named `up`, `down`, or `ls` is shadowed at the root alias; use the
explicit form `cmdman compose mux up <name>` in that case.

## Subcommands

### up

Open or cycle the compose project dashboard. Reads the full compose file's
`mux:` section, resolves each leaf against the cmdman store for the project,
and applies the selected layout to the owned window.

With no `LAYOUT` argument, successive invocations cycle through declared
layouts. Pass a layout name or zero-based index to pin a specific layout.

Inside tmux, the current session is targeted by default. Outside tmux, the
session defaults to `cmdman` and an attach command is printed.

### down

Tear down the cmdman-owned dashboard windows matching this compose project.
The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The services and their monitors
keep running — only the disposable viewers are torn down.

Window discovery is server-wide with no dependence on `$TMUX`: `down` works
from any pane, from `run-shell`, or from outside tmux entirely. `--session`
narrows the scan to one session.

`down` needs no cmdman service or leaf resolution — only the project identity
derived from the compose file (workdir and project name) is required.

Each restored window prints one line:

```
Restored window <name> (<id>) in session <session>
```

Zero matches prints a note and exits 0:

```
No cmdman dashboard found for identity "<identity>"
No cmdman dashboard found for identity "<identity>" in session "<session>"
```

### ls

List cmdman-owned dashboard windows for this compose project. Discovery is
server-wide and requires no `$TMUX` context; it works from any pane, from
`run-shell`, or outside tmux. `--session` narrows the listing to one session.

Unlike standalone `mux ls`, `compose mux ls` filters to the resolved project
identity, so only windows belonging to this project are shown. Listing targets
the server selected by the spec's `driver` and `driver_opt` (including a
custom socket), the same server `compose mux down` queries.

Columns: `SESSION`, `WINDOW`, `ID`, `IDENTITY`, `LAYOUT`. The `LAYOUT` column
shows the last applied layout index; `-1` (no layout applied yet) is displayed
as `-`.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

### up / root alias

- `-s, --session NAME`: target tmux session. Defaults to the current tmux
  session when inside tmux, otherwise `cmdman`.

### down

- `-s, --session NAME`: narrow teardown to this tmux session only. Default:
  server-wide scan.

### ls

- `-s, --session NAME`: narrow listing to this tmux session only. Default:
  server-wide scan.
- `--format FORMAT`: output format. `table` (default) or a Go
  `text/template` string applied per row. Template fields: `.SessionName`,
  `.WindowName`, `.WindowID`, `.Identity`, `.Marker` (int; `-1` means no
  layout applied). Extra template function: `muxMarker` (renders `-1` as
  `"-"`). Standard template functions: `cell`, `command`, `deref`,
  `exitCode`, `fit`, `join`, `json`, `pad`, `shortID`, `trunc`, `width`.

## Compose Mux Example

```yaml
mux:
  driver: tmux
  layouts:
    - name: dev
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: api
            focus: true
          - command: worker
            mode: logs
            cmd_opt: {title: worker-log}
```

```sh
# Open (or cycle) the compose dashboard
cmdman compose mux
cmdman compose mux up

# Apply a specific layout by name
cmdman compose mux dev

# Tear down the dashboard; services keep running
cmdman compose mux down

# Tear down from outside tmux, narrowing to one session
cmdman compose mux down --session work

# List all compose project dashboard windows
cmdman compose mux ls

# Custom output format
cmdman compose mux ls --format '{{.SessionName}} {{.WindowName}} {{muxMarker .Marker}}'
```

## See Also

[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose(5)](./cmdman-compose.5.md), [cmdman-compose-attach(1)](./cmdman-compose-attach.1.md)
