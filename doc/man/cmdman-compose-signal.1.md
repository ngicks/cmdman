# cmdman-compose-signal(1)

## Name

`cmdman compose signal` - send a raw signal to project commands

## Synopsis

```text
cmdman compose [selection flags] signal --signal SIGNAL [--scale N] [COMMAND...]
```

## Description

Sends a numeric or symbolic signal to selected services. With no service names,
all project commands are targeted. This is a raw signal operation: it does not
wait, escalate, or suppress configured restart policy as a graceful stop does.

The signal is required and accepts forms such as `SIGTERM`, `TERM`, `HUP`, and
`15`. Results are reported per replica. A replica of a scaled command is
labeled `<command>-<index>`, and the replica of an unscaled command is labeled
by the command name.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `-s, --signal SIGNAL`: required signal to send. Accepts symbolic names with
  or without `SIG` and numeric signal values.
- `--scale N`: signal only replica N (1-based) of exactly one COMMAND. N must
  name an existing replica.

## See Also

[cmdman-signal(1)](./cmdman-signal.1.md), [cmdman-compose-stop(1)](./cmdman-compose-stop.1.md)
