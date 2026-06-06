# cmdman-restart(1)

## Name

`cmdman restart` - stop and start one or more commands

## Synopsis

```text
cmdman restart [--signal SIGNAL] [--timeout SECONDS] ID|NAME...
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

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-up(1)](./cmdman-compose-up.1.md)
