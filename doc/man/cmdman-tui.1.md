# cmdman-tui(1)

## Name

`cmdman tui` - interactively inspect and control compose-managed commands

## Synopsis

```text
cmdman tui
cmdman tui --popup[=tmux]
cmdman tui --workdir DIR
cmdman tui widget switcher|statusbar|launcher [--workdir DIR] [--no-quit]
```

## Description

Starts the terminal UI over the same data and runtime directories used by the
CLI. The TUI focuses on compose projects and their managed commands, providing
project navigation, command actions, previews, and mux layout cycling.

The TUI can also be launched in a multiplexer popup. Driver inference uses the
current environment; v1 implements tmux only. The popup launcher and child
communicate over an internal IPC endpoint.

## Subcommands

`cmdman tui` with no subcommand is unchanged: it opens the full dashboard
described above.

### widget

Run one TUI widget on its own — a single view filling the terminal, with no tab
bar and no tab switching. A frame definition referencing a built-in component
resolves to exactly this invocation, so a widget is also debuggable by hand: run
it in any terminal or pane. Each widget is its own subcommand.

- `switcher`: every known compose project — running, exited, and never run —
  each heading a group with its commands listed under it. `j`/`k` (or the arrow
  keys) move the selection; `enter` and a left mouse click switch the client to
  the selected project's window and mark that project's bells read; `z` takes
  the frame around the current window down (a window with no frame up is left
  alone); `q` quits. The switcher navigates only — starting, stopping and
  removing commands stay in the full dashboard. A project with no window of its
  own is reported on the hint line rather than brought up.
- `statusbar`: a single line — the working directory's compose project on the
  left, the counts across every project next to it, and the cmdman version at
  the right edge. Sized for a one-row pane; `q` quits.
- `launcher`: quick-launch selector. The left pane lists target locations (the
  directories you have brought projects up in, most recent first, plus
  everything the filter reaches); the right pane lists the compose projects at
  the location under the cursor, toggled on or off. Type to filter, tab
  completes the path, enter steps input → locations → projects, esc walks back
  and then dismisses. On a list, `s` starts the enabled projects and `S`
  launches and lands in one; in the input every key is text, so ctrl+c is the
  dismissal that works from anywhere (unless `--no-quit` took the quit keys
  away).

A widget fills its window, so popup framing belongs to the multiplexer:

```sh
bind-key -n M-Space display-popup -E -w 80% -h 60% 'cmdman tui widget launcher'
```

## Options

### tui

- `--popup[=tmux|zellij]`: run the TUI in a multiplexer popup. A bare
  `--popup` infers the driver from the environment; v1 supports tmux.
- `-w, --workdir DIR`: override the effective work directory used to discover
  the cwd-active compose project. Without it the process working directory is
  used.

### widget

- `-w, --workdir DIR`: as above. Every widget subcommand accepts it.
- `--no-quit`: unbind the quit keys, so no keypress ends the widget. A widget
  invoked as a frame component always runs with it: a frame pane whose widget
  exited would stand empty until the frame is taken down and put back up.

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose-mux(1)](./cmdman-compose-mux.1.md),
[cmdman-mux(1)](./cmdman-mux.1.md)
