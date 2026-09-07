# cmdman-rm(1)

## Name

`cmdman rm` - remove command records and their persisted command data

## Synopsis

```text
cmdman rm [--force] [--label KEY=VALUE] [--ignore-errors] [ID|NAME...]
```

## Description

Removes stopped commands from cmdman's store. Successful removals print the
removed IDs. Targets may be selected explicitly, by repeatable labels, or by a
combination of both.

Running commands are rejected by default. Forced removal sends `SIGKILL`; it is
not a graceful stop and should not be used when the process needs cleanup time.
Per-target errors are reported without preventing other selected commands from
being attempted.

## Options

- `-l, --label KEY=VALUE`: select commands matching a label. Repeatable.
- `-f, --force`: remove running commands by killing their monitor with
  `SIGKILL`.
- `--ignore-errors`: exit 0 even when some targets failed. Failures are still
  printed.

## Examples

```sh
cmdman rm completed-job
cmdman rm --label role=worker
cmdman rm --force stuck-command
```

## Exit Status

- `0`: cmdman removed every selected command.
- `1`: at least one target failed. cmdman prints one line per failure in the
  form `rm <id>: <reason>`, then the summary
  `error: one or more rm operations failed`. `--ignore-errors` turns this exit
  into `0`.

Errors that abort the whole call keep their non-zero exit under
`--ignore-errors`. A named target that no command matches, a selection that
resolves to no command at all, a malformed `--label` value, and a store that
cannot be opened are such errors.

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
