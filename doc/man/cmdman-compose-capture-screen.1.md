# cmdman-compose-capture-screen(1)

## Name

`cmdman compose capture-screen` - capture a snapshot of a compose service's screen

## Synopsis

```text
cmdman compose [selection flags] capture-screen [flags] SERVICE
```

## Description

Resolves `SERVICE` within the selected project and writes a snapshot of its
terminal screen to standard output. Unlike the project-wide compose operations
this targets exactly one replica: a capture is a single screen, so there is
nothing to aggregate.

The flags mirror tmux's `capture-pane` and behave like direct
[`cmdman capture-screen`](./cmdman-capture-screen.1.md): the target must have
`tty: true` and be running, the default range is the visible screen, `-S` and
`-E` share one line index space where `0` is the top visible row and negative
numbers reach into history, and out-of-range values are clamped. A service
without a TTY is rejected; read its output with
[`cmdman compose logs`](./cmdman-compose-logs.1.md).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

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
- `--scale N`: scale index (1-based) of the replica to capture; required when
  the service has more than one replica.

## Examples

```sh
cmdman compose capture-screen api
cmdman compose capture-screen -S -100 api
cmdman compose capture-screen --scale 2 -e worker > worker2.ansi
```

## See Also

[cmdman-capture-screen(1)](./cmdman-capture-screen.1.md),
[cmdman-compose-send-keys(1)](./cmdman-compose-send-keys.1.md),
[cmdman-compose-logs(1)](./cmdman-compose-logs.1.md)
