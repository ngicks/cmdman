# cmdman-compose-create(1)

## Name

`cmdman compose create` - reconcile compose definitions without starting them

## Synopsis

```text
cmdman compose [selection flags] create [--remove-orphan] [--scale N] [COMMAND...]
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

Create runs `create_pre` and `create_post` around the creation of each new
replica. A recreate runs the stop hooks stored on the old replica when it is
running, then its stored remove hooks, then the create hooks of the new
replica. A surplus replica of a scale-down runs its stored stop hooks and
remove hooks. With `--remove-orphan`, a stopped orphan runs its stored remove
hooks. A hook that fails under `on_error: fail` ends the action of its replica
at that hook and makes create exit non-zero. See
[Lifecycle Hooks](./cmdman-compose.5.md#lifecycle-hooks).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--remove-orphan`: remove stopped orphan commands before reconciliation.
  Running orphans are skipped.
- `--scale N`: reconcile only replica N (1-based) of exactly one COMMAND. N must
  lie within the command's declared `scale`. Its `after` dependencies are still
  reconciled in full, and surplus replicas left by a scale-down are kept.

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
