# cmdman-signal(1)

## Name

`cmdman signal` - send a signal without changing lifecycle intent

## Synopsis

```text
cmdman signal --signal SIGNAL [--ignore-errors] ID|NAME...
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
- `--ignore-errors`: exit 0 even when some targets failed. Failures are still
  printed.

## Exit Status

- `0`: cmdman delivered the signal to every target.
- `1`: at least one target failed. cmdman prints one line per failure in the
  form `signal <id>: <reason>`, then the summary
  `error: one or more signal operations failed`. `--ignore-errors` turns this
  exit into `0`. An unknown target and a command without a live monitor are
  per-target failures. The flag covers them too.

Errors that abort the whole call keep their non-zero exit under
`--ignore-errors`. An unparsable `--signal` value and a store that cannot be
opened are such errors.

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-signal(1)](./cmdman-compose-signal.1.md)
