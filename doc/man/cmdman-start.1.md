# cmdman-start(1)

## Name

`cmdman start` - start a command in `created` state

## Synopsis

```text
cmdman start ID|NAME
```

## Description

Starts the stored command definition by spawning its detached monitor. This
command accepts exactly one target and is intended for newly created commands.
It does not modify the stored argv, environment, restart policy, or logging
configuration.

Starting an already running or otherwise ineligible command is an error. For a
stop-and-start operation on a running command, use `cmdman restart`.

## See Also

[cmdman-create(1)](./cmdman-create.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
