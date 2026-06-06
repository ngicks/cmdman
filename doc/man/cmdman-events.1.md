# cmdman-events(1)

## Name

`cmdman events` - query or follow lifecycle events

## Synopsis

```text
cmdman events [flags]
```

## Description

Reads cmdman's global on-disk event log. By default it follows new events. When
following without time bounds, historical records are skipped so the stream
begins at the current end of the log.

Event types include command lifecycle states such as `created`, `starting`,
`running`, `exited`, and `failed`.

JSON or templates are the stable formats for scripted consumption.

## Options

- `--no-follow`: read existing matching entries and exit.
- `--since TIME`: lower time bound. Accepts `now`, RFC3339, or a duration
  offset such as `-5m`.
- `--until TIME`: upper time bound. Accepts the same forms as `--since`.
- `--id ID`: filter by command ID. Repeatable.
- `--type TYPE`: filter by event type. Repeatable.
- `--format FORMAT`: built-in output, `json`, or a Go template.

## Examples

```sh
cmdman events
cmdman events --no-follow --since -1h --type failed
cmdman events --id COMMAND_ID --format json
```

## See Also

[cmdman-logs(1)](./cmdman-logs.1.md), [cmdman-compose-events(1)](./cmdman-compose-events.1.md)
