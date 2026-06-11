# cmdman-mux(1)

## Name

`cmdman mux` - open, tear down, or list tmux dashboards for managed commands

## Synopsis

```text
cmdman mux [--session NAME] [PATH|-]
cmdman mux up [--session NAME] [PATH|-]
cmdman mux down [--session NAME] [PATH]
cmdman mux ls [--session NAME] [--format FORMAT] [PATH]
```

## Description

Reads a YAML document with a top-level `mux:` section and resolves each leaf's
`command` as a cmdman ID or name. A default leaf runs sticky `cmdman attach`;
`mode: logs` runs sticky logs instead. The supervised commands remain owned by
their detached monitors: destroying the tmux window only destroys viewers.

When invoked inside tmux, `mux up` targets the current session by default and
may reuse a safe current window. Outside tmux it creates or updates a detached
session named `cmdman` and prints an attach command. The v1 driver is tmux;
`driver_opt.path` and `driver_opt.socket` can select a binary or isolated tmux
server.

Layouts are trees of horizontal (`h`) or vertical (`v`) containers with
weighted `splits` and leaf panes. Standalone mux cycles layouts on successive
applications. Duplicate commands within one layout are rejected.

The mux file format is documented in [cmdman-mux(5)](./cmdman-mux.5.md).

The root `mux` command is an alias of `mux up`. A layout file path that
happens to be named `up`, `down`, or `ls` shadows the subcommand; use the
explicit form `cmdman mux up <path>` in that case.

## Subcommands

### up

Open or cycle the dashboard. Reads the full spec, resolves each leaf against
the cmdman store, and applies the layout to the owned window. The spec is
read from `PATH` when given, otherwise from stdin (use `-` explicitly for
stdin). Each invocation advances the cycle position; pass a layout name or
zero-based index as part of the spec to pin a specific layout.

`up` targets the current tmux session when run inside tmux. Running it from
`run-shell` or the tmux command prompt may not resolve the correct session;
use `--session` explicitly in those contexts.

### down

Tear down the owned dashboard window matching this spec's identity. The
in-pane viewers are detached, the window collapses to a single clean pane,
and the tmux options cmdman set are cleared. The supervised commands keep
running — only the disposable viewers are torn down.

The spec path is optional: when given it is read only to extract `driver` and
`driver_opt` (for example a custom socket). With no path or the stdin default
`-`, teardown uses the default driver.

Window discovery is server-wide with no dependence on `$TMUX`: `down` works
from any pane, from `run-shell`, or from outside tmux entirely.
`--session` narrows the scan to one session.

Each restored window prints one line:

```
Restored window <name> (<id>) in session <session>
```

Zero matches prints a note and exits 0:

```
No cmdman dashboard found for identity "<identity>"
No cmdman dashboard found for identity "<identity>" in session "<session>"
```

**Known limitation:** Standalone `mux down` derives its search identity from
the window name, which defaults to the session name. A dashboard built with
the default window name in a different session resolves a different identity,
so server-wide `down` will not find it. Use `--session` to narrow the scan,
or use the explicit `mux up --session NAME` form when building the dashboard.
`compose mux down` is unaffected — its identity is derived from
`workdir + project` and is stable across sessions.

### ls

List all cmdman-owned dashboard windows on the server.

Discovery is server-wide and requires no `$TMUX` context; it works from any
pane, from `run-shell`, or outside tmux. `--session` narrows the listing to
one session.

The spec path is optional: when given it is read only to extract `driver` and
`driver_opt` (for example a custom socket). With no path or the stdin default
`-`, listing uses the default driver with no custom options.

Columns: `SESSION`, `WINDOW`, `ID`, `IDENTITY`, `LAYOUT`. The `LAYOUT` column
shows the last applied layout index; `-1` (no layout applied yet) is displayed
as `-`.

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

## Example

```yaml
mux:
  driver: tmux
  layouts:
    - name: main
      root:
        dir: h
        splits: [2, 1]
        panes:
          - command: api
          - command: worker
            mode: logs
```

```sh
# Open (or cycle) the dashboard
cmdman mux dashboard.yaml
cmdman mux up dashboard.yaml

# Tear down the dashboard; supervised commands keep running
cmdman mux down dashboard.yaml

# Tear down without a spec file (uses default driver)
cmdman mux down

# List all owned dashboard windows
cmdman mux ls

# List owned windows in a specific session
cmdman mux ls --session work

# Tear down from outside tmux, narrowing to one session
cmdman mux down --session work dashboard.yaml
```

## See Also

[cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose-mux(1)](./cmdman-compose-mux.1.md), [cmdman-attach(1)](./cmdman-attach.1.md)
