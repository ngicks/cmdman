# cmdman-logs(1)

## Name

`cmdman logs` - read a command's persistent log

## Synopsis

```text
cmdman logs [flags] ID|NAME
```

## Description

Reads records from the command's configured log driver. This differs from
`attach`: logs work for non-TTY commands, preserve stdout/stderr stream
identity, and do not accept input.

Records can be filtered by time range and limited from the head or tail after
filtering. Ordinary following ends with the active log stream. Sticky following
keeps watching across command restarts.

Commands using the `none` log driver have no persistent records to read.

## Options

- `-f, --follow`: continue reading live output after existing records.
- `--since TIME`: lower time bound. Accepts `now`, RFC3339, or a duration
  offset such as `-5m`.
- `--until TIME`: upper time bound. Accepts the same forms as `--since`.
- `--head N`: show at most the first N log lines after filtering.
- `--tail N`: show at most the last N log lines after filtering.
- `--sticky`: follow across command restarts and inject lifecycle meta lines.
- `--meta-prefix PREFIX`: prefix for sticky meta lines. Defaults to `#|`.

## Examples

```sh
cmdman logs --tail 100 api
cmdman logs --follow --since -5m api
cmdman logs --sticky worker
```

## See Also

[cmdman-attach(1)](./cmdman-attach.1.md), [cmdman-events(1)](./cmdman-events.1.md)
