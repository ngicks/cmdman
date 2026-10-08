# cmdman-restart(1)

## Name

`cmdman restart` - stop and start one or more commands

## Synopsis

```text
cmdman restart [--signal SIGNAL] [--timeout DURATION] [--ignore-errors] ID|NAME...
```

## Description

Performs an explicit stop followed by a start using the existing stored command
definition. It does not reread executable configuration or a compose file.
Use `cmdman compose up` to reconcile changed compose configuration.

The stop phase follows the same signal, timeout, process-group, and forced-kill
rules as `cmdman stop`. Targets are processed independently and per-target
failures are reported on stderr.

## Options

- `-s, --signal SIGNAL`: signal to send during the stop phase. When omitted,
  each command's stored stop signal is used.
- `-t, --timeout DURATION`: time to wait after the stop signal before sending
  `SIGKILL`. Give integer seconds or a Go duration such as `1m30s`. The value
  must be positive. When omitted, each command waits the stop timeout stored
  with it, or 10 seconds when it has none, as in
  [cmdman-stop(1)](./cmdman-stop.1.md).
- `--ignore-errors`: exit 0 even when some targets failed. Failures are still
  printed.

## Exit Status

- `0`: cmdman restarted every target. A command that was not running is only
  started.
- `1`: at least one target failed. cmdman prints one line per failure in the
  form `restart <id>: <reason>`, then the summary
  `error: one or more restart operations failed`. `--ignore-errors` turns this
  exit into `0`.

Errors that abort the whole call keep their non-zero exit under
`--ignore-errors`. An unknown target, an unparsable `--signal` value, an
unparsable or non-positive `--timeout` value, and a store that cannot be opened
are such errors. A rejected `--signal` or `--timeout` value restarts no command.

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-up(1)](./cmdman-compose-up.1.md)
