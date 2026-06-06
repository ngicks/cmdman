# cmdman-rm(1)

## Name

`cmdman rm` - remove command records and their persisted command data

## Synopsis

```text
cmdman rm [--force] [--label KEY=VALUE] [ID|NAME...]
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

## Examples

```sh
cmdman rm completed-job
cmdman rm --label role=worker
cmdman rm --force stuck-command
```

## See Also

[cmdman-stop(1)](./cmdman-stop.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
