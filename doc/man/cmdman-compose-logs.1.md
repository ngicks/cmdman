# cmdman-compose-logs(1)

## Name

`cmdman compose logs` - merge logs from project commands

## Synopsis

```text
cmdman compose [selection flags] logs [flags] [COMMAND...]
```

## Description

Reads logs for selected project services and merges their records into one
stream. Each rendered line identifies its originating compose command. With no
names, all project commands are selected.

Records are selected per command and then merged for display. Compose logs has
no sticky-across-restart mode.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `--follow`: continue reading live output after existing records.
- `--since RFC3339`: lower time bound.
- `--until RFC3339`: upper time bound.
- `--head N`: return only the first N records per command.
- `--tail N`: return only the last N records per command.

## Examples

```sh
cmdman compose logs --tail 50
cmdman compose logs --follow api worker
```

## See Also

[cmdman-logs(1)](./cmdman-logs.1.md), [cmdman-compose-events(1)](./cmdman-compose-events.1.md)
