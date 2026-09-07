# cmdman-restart(1)

## Name

`cmdman restart` - stop and start one or more commands

## Synopsis

```text
cmdman restart [--signal SIGNAL] [--timeout SECONDS] [--ignore-errors] ID|NAME...
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
- `-t, --timeout SECONDS`: seconds to wait before sending `SIGKILL`. Defaults
  to 10.
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
`--ignore-errors`. An unknown target, an unparsable `--signal` value, and a
store that cannot be opened are such errors.

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-up(1)](./cmdman-compose-up.1.md)
