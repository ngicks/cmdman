# cmdman-tui(1)

## Name

`cmdman tui` - interactively inspect and control compose-managed commands

## Synopsis

```text
cmdman tui
cmdman tui --popup[=tmux]
cmdman tui --workdir DIR
```

## Description

Starts the terminal UI over the same data and runtime directories used by the
CLI. The TUI focuses on compose projects and their managed commands, providing
project navigation, command actions, previews, and mux layout cycling.

The TUI can also be launched in a multiplexer popup. Driver inference uses the
current environment; v1 implements tmux only. The popup launcher and child
communicate over an internal IPC endpoint.

## Options

- `--popup[=tmux|zellij]`: run the TUI in a multiplexer popup. A bare
  `--popup` infers the driver from the environment; v1 supports tmux.
- `-w, --workdir DIR`: override the effective work directory used to discover
  the cwd-active compose project. Without it the process working directory is
  used.

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose-mux(1)](./cmdman-compose-mux.1.md)
