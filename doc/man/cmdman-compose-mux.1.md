# cmdman-compose-mux(1)

## Name

`cmdman compose mux` - open, tear down, list, or cycle replica positions for tmux dashboards

## Synopsis

```text
cmdman compose [selection flags] mux [--session NAME] [LAYOUT]
cmdman compose [selection flags] mux up [--session NAME] [LAYOUT]
cmdman compose [selection flags] mux down [--session NAME]
cmdman compose [selection flags] mux ls [--session NAME] [--format FORMAT]
cmdman compose [selection flags] mux cycle-scale [--session NAME] <command>[=N]
```

## Description

Reads the selected compose file's top-level `mux:` section. Each leaf names a
compose service and resolves to the stored cmdman command for the selected
project. Default leaves run sticky attach viewers; `mode: logs` leaves run
sticky log viewers. Mux never moves command ownership into tmux, so closing the
window does not stop the services.

The embedded `mux:` section uses the same layout format documented in
[cmdman-mux(5)](./cmdman-mux.5.md), except that `command` values name compose
services in the selected project. When a compose command has a `scale:` field
(multiple replicas), mux leaves referencing it may be *pinned* or
*cycle-scale targets*:

- **Pinned** (`scale: N` in the leaf): always shows replica N. Never changed by
  `cycle-scale`.
- **Cycle-scale target** (no `scale:` in the leaf, or `scale: 0`): shows the
  replica recorded in the dashboard window's `@cmdman_scale` option. Advance
  with `compose mux cycle-scale`. Defaults to replica 1 when no position has
  been stored.

Layout cycling and replica cycling are independent. `mux up` cycles layouts;
`cycle-scale` advances one command's replica. Replica positions persist across
layout switches and are cleared by `mux down`.

Without `LAYOUT`, successive invocations of `mux up` cycle through declared
layouts. A layout can be selected by name or zero-based index; names take
precedence over numeric interpretation. The selected index becomes the next
cycle position.

Inside tmux, the current session is selected by default. Outside tmux, the
session defaults to `cmdman` and an attach hint is printed. The owned window is
named `cmdman-PROJECT`.

When no compose file is explicitly selected, `compose mux` selects a compose
associated with the current directory that declares a mux section. Ambiguous
selection is an error. Unlike other compose commands, a missing mux section is
always an error.

The root `compose mux` command is an alias of `compose mux up`. A layout
literally named `up`, `down`, `ls`, or `cycle-scale` is shadowed at the root
alias; use the explicit form `cmdman compose mux up <name>` in that case.

## Subcommands

### up

Open or cycle the compose project dashboard. Reads the full compose file's
`mux:` section, resolves each leaf against the cmdman store for the project,
and applies the selected layout to the owned window.

Before applying the layout, `up` reads the current replica positions from the
dashboard window's `@cmdman_scale` option, so cycle-scale advances made since
the last `up` are honoured when `up` next re-builds the window.

With no `LAYOUT` argument, successive invocations cycle through declared
layouts. Pass a layout name or zero-based index to pin a specific layout.

Inside tmux, the current session is targeted by default. Outside tmux, the
session defaults to `cmdman` and an attach command is printed.

### down

Tear down the cmdman-owned dashboard windows matching this compose project.
The in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared — including the stored replica
positions (`@cmdman_scale`). The services and their monitors keep running —
only the disposable viewers are torn down.

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

`compose mux ls` opens the cmdman store to resolve live replica counts for the
SCALE column. If the store is unavailable or a command has no live replicas,
the count renders as `?`.

Columns: `SESSION`, `WINDOW`, `ID`, `IDENTITY`, `LAYOUT`, `SCALE`.

- The `LAYOUT` column shows the last applied layout index; `-1` (no layout
  applied yet) is displayed as `-`.
- The `SCALE` column shows per-window cycle-scale positions and live replica
  counts. Format: space-separated `cmd=pos/count` pairs sorted by command name
  (for example `web=2/3 worker=1/2`). Commands whose count cannot be resolved
  render as `cmd=pos/?`. Windows with no cycle-scale targets render `-`.

### cycle-scale

Advance the replica shown for a compose service in the mux dashboard.

`cycle-scale` requires exactly one argument: a compose service name, optionally
followed by `=N` (1-based replica number). Shell completion offers the spec's
unpinned leaf command names.

Without `=N`, the command advances to the next replica, wrapping from the last
back to the first. With `=N`, the pane jumps directly to replica N.

The new position is stored in the dashboard window's `@cmdman_scale` option and
persists across layout switches. It is cleared by `compose mux down`.

The target pane is located by the `@cmdman_leaf` option stamped on each pane
by `compose mux up`. If the command is not visible in the current layout, the
position is still updated and takes effect on the next `compose mux up`. The
output line for that window includes an advisory notice:

```
<session>:<window> <command> -> <command>-<N> (not visible in layout "<name>")
```

When the command is visible, the output is:

```
<session>:<window> <command> -> <command>-<N>
```

Window discovery is server-wide with no `$TMUX` dependency. `--session` narrows
the operation to one session.

Only unpinned leaves (no `scale:` in the `mux:` section) are cycle-scale
targets. A leaf with an explicit `scale: N` is pinned and is never advanced.

**Errors:**

- `mux: "<command>" is not a cycle-scale target: not an unpinned leaf in any layout`:
  the command has no unpinned leaf in any layout of the spec.
- `mux: no dashboard window found; run "cmdman compose mux up" first`: no owned
  window was found for this project.
- `mux: position N is out of range [1,<count>]`: the explicit `=N` value exceeds
  the live replica count.
- `mux: position N is pinned in current layout`: another leaf in the current
  layout already pins that exact replica index; the advance would create a
  duplicate.
- `mux: all scale positions for command are pinned in current layout`: every
  available replica is pinned by other leaves; no unpinned slot to advance to.

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
  layout applied), `.Scale` (string; precomputed SCALE column value,
  `cmd=pos/count` pairs or `"-"`). Extra template function: `muxMarker`
  (renders `-1` as `"-"`). Standard template functions: `cell`, `command`,
  `deref`, `exitCode`, `fit`, `join`, `json`, `pad`, `shortID`, `trunc`,
  `width`.

### cycle-scale

- `-s, --session NAME`: narrow operation to this tmux session only. Default:
  server-wide.

## Compose Mux Example

```yaml
name: myapp
commands:
  web:
    args: ["./web"]
    scale: 3          # three replicas: web-1, web-2, web-3
  worker:
    args: ["./worker"]

mux:
  driver: tmux
  layouts:
    - name: dev
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: web    # unpinned: cycle-scale target, defaults to web-1
            focus: true
          - command: worker
            mode: logs
            cmd_opt: {title: worker-log}
    - name: web-detail
      root:
        dir: h
        splits: [1, 1, 1]
        panes:
          - command: web
            scale: 1        # pinned: always shows web-1
          - command: web    # unpinned: cycle-scale target
          - command: worker
```

```sh
# Open (or cycle) the compose dashboard
cmdman compose mux
cmdman compose mux up

# Apply a specific layout by name
cmdman compose mux dev

# Advance 'web' to its next replica (1 -> 2 -> 3 -> 1)
cmdman compose mux cycle-scale web

# Jump 'web' directly to replica 3
cmdman compose mux cycle-scale web=3

# Tear down the dashboard; replica positions are cleared; services keep running
cmdman compose mux down

# Tear down from outside tmux, narrowing to one session
cmdman compose mux down --session work

# List all compose project dashboard windows (with live replica counts)
cmdman compose mux ls

# Custom output format
cmdman compose mux ls --format '{{.SessionName}} {{.WindowName}} {{muxMarker .Marker}} {{.Scale}}'
```

## See Also

[cmdman-mux(1)](./cmdman-mux.1.md), [cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose(5)](./cmdman-compose.5.md), [cmdman-compose-attach(1)](./cmdman-compose-attach.1.md)
