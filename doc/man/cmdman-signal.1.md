# cmdman-signal(1)

## Name

`cmdman signal` - send a signal without changing lifecycle intent

## Synopsis

```text
cmdman signal --signal SIGNAL ID|NAME...
```

## Description

Sends the requested numeric or symbolic signal to each running command. Unlike
`stop`, this command does not wait, escalate to `SIGKILL`, or mark the signal
as an explicit stop. If the signal causes the process to exit, its configured
restart policy may restart it.

Signals accept forms such as `TERM`, `SIGTERM`, `15`, and `HUP`. Failures for
individual targets are written to stderr while remaining targets are attempted.

## Options

- `-s, --signal SIGNAL`: required signal to send. Accepts symbolic names with
  or without `SIG` and numeric signal values.

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-signal(1)](./cmdman-compose-signal.1.md)
