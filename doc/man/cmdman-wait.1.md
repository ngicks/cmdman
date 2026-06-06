# cmdman-wait(1)

## Name

`cmdman wait` - wait for commands to reach a state

## Synopsis

```text
cmdman wait [--condition STATE] [--interval DURATION] [--ignore] ID|NAME...
```

## Description

Polls every target until it reaches the selected condition. Supported
conditions are `stopped`, `created`, `starting`, `running`, `exited`, and
`failed`; `stopped` is the default aggregate terminal condition.

One line is printed per target, preserving the result order. Terminal commands
print their exit code; conditions without an exit code print `0`. Missing or
unresolvable commands fail the operation by default.

This command is suitable for scripts coordinating detached commands:

```sh
id=$(cmdman run -- sh -c 'sleep 1; exit 7')
cmdman wait "$id"
```

## Options

- `-c, --condition STATE`: wait condition. Defaults to `stopped`.
- `-i, --interval DURATION`: polling interval. Defaults to `250ms`.
- `--ignore`: do not fail on missing command errors.

## See Also

[cmdman-inspect(1)](./cmdman-inspect.1.md), [cmdman-compose-wait(1)](./cmdman-compose-wait.1.md)
