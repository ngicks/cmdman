# cmdman-compose-create(1)

## Name

`cmdman compose create` - reconcile compose definitions without starting them

## Synopsis

```text
cmdman compose [selection flags] create [--remove-orphan] [COMMAND...]
```

## Description

Loads and normalizes the compose file, computes a reconciliation plan, and
creates or recreates selected commands. Matching commands are unchanged.
Changed running commands are stopped before recreation.

With command names, those commands and their recursive `after` dependencies are
reconciled so the selected commands can later be started with everything they
need. With no names, every declared command is reconciled.

Commands belonging to the project but absent from the desired file are
orphans. They are retained by default. Use `compose down` for destructive
whole-project teardown.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `--remove-orphan`: remove stopped orphan commands before reconciliation.
  Running orphans are skipped.

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
