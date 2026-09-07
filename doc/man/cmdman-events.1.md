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

## Event Attributes

An event carries an optional `attrs` map of string values. The map is absent
when the event has nothing extra to report.

- `signal`: the signal number. It appears on `stopped` and `signaled` events.
- `restart_count`: the decimal restart counter. It appears on the `starting`
  event of a restart, never on the first start.

Two attributes describe an anomaly at the end of a run. They appear on the
run's terminal `exited` or `failed` event and are absent when nothing went
wrong. Neither anomaly stops the run from ending. The monitor log carries the
same warning, and [`cmdman inspect`](./cmdman-inspect.1.md) lists it under the
command's `warnings`.

- `reader_detached`: the literal `true`. The pty reader was still blocked one
  second after the child exited. A leftover process that kept the terminal
  open is the usual cause. Trailing output may be missing from the log.
- `survivors_unreaped`: a decimal count. It reports the processes still alive
  when the survivor sweep gave up after its 10 second bound.

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
