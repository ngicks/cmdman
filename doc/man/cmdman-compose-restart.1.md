# cmdman-compose-restart(1)

## Name

`cmdman compose restart` - stop and restart stored project commands

## Synopsis

```text
cmdman compose [selection flags] restart [COMMAND...]
```

## Description

Restarts the selected existing commands using their stored definitions. It does
not reconcile changes from the compose file; use `compose up` for that.

The stop phase follows reverse dependency order and the start phase follows
forward dependency order; work within each layer is concurrent. Orphans are
skipped. When no compose file is loaded, dependency order is reconstructed from
stored compose labels.

The operation reports outcomes per command. Target selection is project-scoped,
so service names resolve only within the selected `(workdir, project)` pair.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

No command-specific options.

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
