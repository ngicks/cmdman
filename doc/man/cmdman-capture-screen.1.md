# cmdman-capture-screen(1)

## Name

`cmdman capture-screen` - capture a snapshot of a running TTY command's screen

## Synopsis

```text
cmdman capture-screen [flags] ID|NAME
```

## Description

Writes a snapshot of a running command's terminal screen to standard output.
The flags mirror tmux's `capture-pane`; there is no buffer concept, so a
capture always goes to stdout and is redirected or piped like any other output.

Only a command created with a TTY has a screen to capture. A command without
one is rejected: it has only a byte stream, which is read back with
[`cmdman logs`](./cmdman-logs.1.md). A stopped command is rejected as well,
because the screen lives only in the monitor of the current run.

By default the capture covers the visible screen. `-S, --start-line` and
`-E, --end-line` share one line index space: `0` is the topmost visible row and
negative numbers reach into history, which is the terminal emulator's
scrollback. Out-of-range values are clamped to the lines that exist rather than
rejected. The alternate screen has no history of its own.

Trailing unused positions at each line's end are trimmed unless `-N` is given.

## Options

- `-e, --escapes`: include escape sequences for text and background attributes.
- `-a, --alt-screen`: capture the alternate screen; errors when the command has
  none.
- `-q, --quiet`: with `-a`, succeed with empty output when there is no alternate
  screen.
- `-N, --preserve-trailing-spaces`: preserve trailing spaces at each line's end.
- `-S, --start-line N`: first line to capture. `0` is the top visible row,
  negative numbers reach into history, `-` is the start of history.
- `-E, --end-line N`: last line to capture. `-` is the bottom of the visible
  screen.

## Examples

```sh
cmdman capture-screen server
cmdman capture-screen -S -100 server
cmdman capture-screen -S - -E - server
cmdman capture-screen -e server > screen.ansi
```

## See Also

[cmdman-send-keys(1)](./cmdman-send-keys.1.md), [cmdman-logs(1)](./cmdman-logs.1.md),
[cmdman-compose-capture-screen(1)](./cmdman-compose-capture-screen.1.md)
