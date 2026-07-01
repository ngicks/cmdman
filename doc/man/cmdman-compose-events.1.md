# cmdman-compose-events(1)

## Name

`cmdman compose events` - stream lifecycle events for project commands

## Synopsis

```text
cmdman compose [selection flags] events [flags] [COMMAND...]
```

## Description

Filters the global cmdman event log to commands in the selected compose project,
optionally narrowed by service names and event types. With no matching project
commands, the command exits successfully without producing records.

By default it follows new events. When following without time bounds,
historical records are skipped. Time bounds can limit either replayed or
followed records.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--no-follow`: read existing matching entries and exit.
- `--since TIME`: lower time bound. Accepts `now`, RFC3339, or a duration
  offset such as `-5m`.
- `--until TIME`: upper time bound. Accepts the same forms as `--since`.
- `--type TYPE`: filter by event type. Repeatable.
- `--format FORMAT`: built-in output, `json`, or a Go template.

## See Also

[cmdman-events(1)](./cmdman-events.1.md), [cmdman-compose-logs(1)](./cmdman-compose-logs.1.md)
