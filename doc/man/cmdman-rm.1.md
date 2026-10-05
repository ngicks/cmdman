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

Starting and running commands are rejected by default. Forced removal stops
such a command first, the way [cmdman-stop(1)](./cmdman-stop.1.md) does with
its defaults. cmdman sends the command's stop signal to its process group, waits
up to 10 seconds, then sends `SIGKILL`. The monitor sweeps the processes the
command left in its session, as [cmdman(1)](./cmdman.1.md) describes, and cmdman
removes the command once it has stopped.

When cmdman cannot reach the monitor, or the stop fails, cmdman kills the
monitor with `SIGKILL` and removes the command anyway. Processes the command
started can then outlive the removal. Interrupting `rm` while it waits for the
stop leaves the command in the store.

Per-target errors are reported without preventing other selected commands from
being attempted.

## Options

- `-l, --label KEY=VALUE`: select commands matching a label. Repeatable.
- `-f, --force`: stop starting and running commands through their monitor,
  then remove them. cmdman kills the monitor with `SIGKILL` only when it cannot
  reach the monitor or the stop fails.
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
