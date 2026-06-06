# cmdman-compose-down(1)

## Name

`cmdman compose down` - stop and remove compose-managed commands

## Synopsis

```text
cmdman compose [selection flags] down [--progress MODE] [COMMAND...]
```

## Description

Stops selected commands, waits for the stop phase, then removes their stored
records concurrently. It is the destructive counterpart to `compose stop`.

With no command names, down removes the entire selected project, including
orphans that share its `(workdir, project)` labels. Running orphans are stopped
as part of whole-project teardown.

With command names and a loaded file, the target set is each named command plus
its recursive dependents. Only that closure is stopped and removed. Dependency
ordering is used for stopping; removal begins after all stop attempts complete.
Failures are aggregated and do not prevent other targets from being attempted.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise.

## See Also

[cmdman-compose-stop(1)](./cmdman-compose-stop.1.md), [cmdman-compose-create(1)](./cmdman-compose-create.1.md)
