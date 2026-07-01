# cmdman-compose-stop(1)

## Name

`cmdman compose stop` - stop project commands without removing them

## Synopsis

```text
cmdman compose [selection flags] stop [--progress MODE] [COMMAND...]
```

## Description

Gracefully stops selected running commands using each stored command's stop
signal and timeout behavior. Stored definitions remain available for a later
`compose start`.

Naming a command also selects all recursive dependents, and stopping proceeds
in reverse dependency order. With no names, all declared project commands are
stopped. Orphans are not part of the declared graph and are not stopped by this
operation. When no compose file is loaded, dependency order is reconstructed
from stored compose labels.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise.

## See Also

[cmdman-compose-start(1)](./cmdman-compose-start.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
