# cmdman-mux(1)

## Name

`cmdman mux` - open, tear down, or list tmux dashboards for managed commands,
and control the frame around a window

## Synopsis

```text
cmdman mux [--session NAME] [PATH|-]
cmdman mux up [--session NAME] [PATH|-]
cmdman mux down [--session NAME] [PATH]
cmdman mux ls [--session NAME] [--format FORMAT] [PATH]
cmdman mux frame show [--session NAME] [DEF]
cmdman mux frame hide [--session NAME]
cmdman mux frame cycle [--session NAME]
cmdman mux frame ls [--session NAME]
```

## Description

Reads a YAML document with a top-level `mux:` section and resolves each leaf's
`command` as a cmdman ID or name. A default leaf runs sticky `cmdman attach`;
`mode: logs` runs sticky logs instead. The supervised commands remain owned by
their detached monitors: destroying the tmux window only destroys viewers.

When invoked inside tmux, `mux up` targets the current session by default and
may reuse a safe current window. Outside tmux it creates or updates a detached
session named `cmdman` and prints an attach command. The v1 driver is tmux;
`driver.path` and `driver.socket` can select a binary or isolated tmux
server.

Layouts are trees of horizontal (`h`) or vertical (`v`) containers with
weighted `splits` and leaf panes. Standalone `mux up` cycles layouts on
successive applications; each spec layout maps to exactly one applied layout.
Duplicate commands within one layout are rejected.

Standalone `cmdman mux` has no replica counter, so unpinned leaves (`scale:`
absent) resolve the command name without a replica suffix. A pinned leaf
(`scale: N`) resolves the suffixed command `<command>-N`.

The mux file format is documented in [cmdman-mux(5)](./cmdman-mux.5.md).

`mux frame` controls the frame — the components docked around the edges of a
window, described by a frame definition file. The frame is a fixture the
dashboard lives inside: it is per-window state of its own, so `mux up`,
layout cycling and `mux down` leave it standing.

The root `mux` command is an alias of `mux up`. A layout file path that
happens to be named `up`, `down`, `ls`, or `frame` shadows the subcommand; use
the explicit form `cmdman mux up <path>` in that case.

## Subcommands

### up

Open or cycle the dashboard. Reads the full spec, resolves each leaf against
the cmdman store, and applies the layout to the owned window. The spec is
read from `PATH` when given, otherwise from stdin (use `-` explicitly for
stdin). Each invocation advances the layout cycle position; pass a layout name
or zero-based index to select a specific layout directly.

`up` targets the current tmux session when run inside tmux. Running it from
`run-shell` or the tmux command prompt may not resolve the correct session;
use `--session` explicitly in those contexts.

When the `default_frame` config key is set, every window `up` creates gets that
frame shown around it; see [Configuration](#configuration).

### down

Tear down the owned dashboard window matching this spec's identity. The
in-pane viewers are detached and the project's panes and state are removed.
A frame standing on the window is left in place with its own state; on a
window with no frame the window collapses to a single clean pane and every
tmux option cmdman set is cleared. The supervised commands keep running —
only the disposable viewers are torn down.

The spec path is optional: when given it is read only to extract the `driver`
object (for example a custom socket). With no path or the stdin default
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

The spec path is optional: when given it is read only to extract the `driver`
object (for example a custom socket). With no path or the stdin default
`-`, listing uses the default driver with no custom options.

Columns: `SESSION`, `WINDOW`, `ID`, `IDENTITY`, `FRAME`, `LAYOUT`, `SCALE`.

- The `FRAME` column shows the name of the frame def currently shown around the
  window, or `-` when the window is unframed. A window can carry a frame with
  no project (chrome put up before anything was launched, or left standing by
  `mux down`), in which case `IDENTITY` is blank and `FRAME` is what names it.
- The `LAYOUT` column shows the last applied layout index; `-1` (no layout
  applied yet) is displayed as `-`.
- The `SCALE` column shows the replica positions stored on the window as
  `cmd=pos` pairs (sorted by command name), or `-` when none are stored.
  Standalone `mux ls` has no replica counter, so no `/count` suffix is shown.
  Use `compose mux ls` to see `cmd=pos/count` values for compose projects.

### frame

Show, hide, cycle, or list the frame around a window. A frame is a per-window
fixture, so `show`, `hide` and `cycle` act on the window you are sitting in and
must be run inside the multiplexer; outside it they fail with

```
mux: frame: not inside a multiplexer; run this inside tmux or name a session
```

`--session NAME` names a session instead, and then that session's current
window is the target. `frame ls` needs neither: it scans the server.

`DEF` is a bare def name resolved under the frame directory
(`<config-dir>/frame/<name>.yaml` or `.yml`), or a path to a def file.

An entry marked `managed: true` is not run in its pane: it runs as a supervised
cmdman command named `frame-<def>-<i>` (def name plus entry index) and the pane
attaches to it. An entry command already running under that name is adopted
rather than started a second time — including after an edit to the entry, which
keeps the process that is up until it is stopped by hand.

### frame show

Show the frame def `DEF` around the current window.

With no `DEF` the `default_frame` config key is used. With neither, the error
lists the def names found in the frame directory:

```
mux: frame: no def named and default_frame is unset; available defs: alt, dev
```

A bare `DEF` that resolves to no file is reported with the path that was tried —
the reference resolved against the working directory — plus that same list of
candidates. Run from `~/work` with no `dv` def anywhere:

```
error: mux: frame def "dv": open /home/u/work/dv: no such file or directory; available defs: alt, dev
```

A `DEF` given as a path is reported with that path alone — the defs in the frame
directory are not what it meant. So is a `DEF` that exists and fails to parse:
the parse error stands on its own rather than reading as a missing def.

Showing the def already up is a no-op. Showing a different one replaces it in
place — the frame standing on the window is taken down and the new one docked,
which is how a frame is *selected*; there is no separate `select` verb. The def
is read and carved before the multiplexer is touched, so a typo or a broken def
never disturbs the frame already standing.

Nothing is printed on success.

### frame hide

Take the frame around the current window down; the project region expands into
the space it occupied. A window carrying no frame is a quiet no-op, and so is a
`hide` racing the window's disappearance.

Managed entries survive: the panes and their disposable viewers go away, while
each `frame-<def>-<i>` command keeps running under its own monitor and stays
visible in `cmdman ls`. A later `show` attaches to it again instead of starting
a second one. Ephemeral entries (the default) die with their panes and are re-run
by the next `show`.

Nothing is printed on success.

### frame cycle

Show the next frame def around the current window.

The rotation is the sorted list of def names in the frame directory (the
`DEF` rows `frame ls` prints for defs on disk — a def shown by path or
deleted since appears in `ls` but not in the rotation). It starts after the
def currently shown and wraps
around; an unframed window, or one showing a def that is not among them (shown
by path, or deleted since), gets the first def. Each step is a `show`, so it
replaces the frame in place and preserves managed entries exactly as above.

Nothing is printed on success.

### frame ls

List the frame defs and the windows they are shown around.

Discovery is server-wide and requires no `$TMUX` context; it works from any
pane, from `run-shell`, or outside tmux. `--session` narrows the window scan to
one session; the def list is always the full one.

Columns: `DEF`, `WINDOWS`.

- `DEF` is a def name from the frame directory, or the name stamped on a window
  that shows a def which is not discoverable there (shown by path, or deleted
  since) — so nothing standing on a window is missing from the listing.
- `WINDOWS` holds the ids of the windows the def is shown around, space
  separated, or `-` when it is shown nowhere.

```
DEF   WINDOWS
alt   -
dev   @0
```

This listing is def-centric; `mux ls` answers the same question per window in
its `FRAME` column.

### Collapsing from the docked switcher

A docked `switcher` widget binds `z` to take the frame down, and a widget run as
a frame component never quits on a keypress. Both are documented in
[cmdman-tui(1)](./cmdman-tui.1.md) (`widget` subcommand and `--no-quit`).
Putting the frame back up is a CLI step — `cmdman mux frame show` — since the
widget is gone with the frame.

## Configuration

`default_frame` (config file key, `config.json`) names the frame def used when
no def is named: a bare name resolved under the frame directory, or a path.

The key also applies the def: every window an up creates — `cmdman mux up` and
[`cmdman compose mux up`](./cmdman-compose-mux.1.md) alike — gets that frame
shown around it, which is what keeps the frame present across the per-project
windows the switcher jumps between. A window that already carries a frame is
left alone — auto-show never replaces a def chosen deliberately. A def that is
missing or broken warns and never fails the `up`: putting the dashboard up is
what `up` is for.

Unset means no default and no auto-show; `cmdman mux frame show DEF` still
shows a def by name. `cmdman config` prints the resolved value.

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
  `.WindowName`, `.WindowID`, `.Identity`, `.Frame` (string; the frame def
  shown around the window, empty when unframed), `.Marker` (int; `-1` means no
  layout applied), `.Scale` (string; precomputed SCALE column value, stored
  `cmd=pos` pairs or `"-"`). Extra template functions: `muxMarker`
  (renders `-1` as `"-"`), `muxFrame` (renders an unframed window as `"-"`).
  Standard template functions: `cell`, `command`,
  `deref`, `exitCode`, `fit`, `join`, `json`, `pad`, `shortID`, `trunc`,
  `width`.

### frame show / hide / cycle

- `-s, --session NAME`: act on the current window of this tmux session.
  Default: the window the command is run in, which requires being inside the
  multiplexer.

### frame ls

- `-s, --session NAME`: narrow the window scan to this tmux session only.
  Default: server-wide scan. The def list is unaffected.

The frame verbs take no other flags.

## Example

```yaml
mux:
  driver:
    name: tmux
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

```sh
# Dock the "dev" frame around the current window (run inside tmux)
cmdman mux frame show dev

# Dock the configured default_frame instead
cmdman mux frame show

# Walk the defs; each step replaces the frame in place
cmdman mux frame cycle

# Collapse the frame; managed entries keep running
cmdman mux frame hide

# What can I show, and where is it up?
cmdman mux frame ls

# Frame a session's current window from outside tmux
cmdman mux frame show dev --session work
```

## See Also

[cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose-mux(1)](./cmdman-compose-mux.1.md), [cmdman-attach(1)](./cmdman-attach.1.md),
[cmdman-tui(1)](./cmdman-tui.1.md)
