# cmdman-compose-up(1)

## Name

`cmdman compose up` - reconcile and start compose commands

## Synopsis

```text
cmdman compose [selection flags] up [--remove-orphan] [--progress MODE] [--mux] [COMMAND...]
```

## Description

Performs `compose create` followed by dependency-aware start. The operation is
detached: it returns after start processing and does not attach to command
output.

When command names are supplied, reconciliation targets those names and start
pulls in their recursive dependencies. Dependencies run in topological layers;
independent commands start concurrently. An `after` condition controls whether
a dependent waits for its prerequisite to start, complete, or complete
successfully.

Stopped stored project commands absent from the file can be removed during
reconciliation. Progress output can be selected explicitly for deterministic CI
logs.

Failures are aggregated so unrelated commands can still be attempted.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--remove-orphan`: remove stopped orphan commands before reconciliation.
  Running orphans are skipped.
- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise.
- `--mux`: after a successful up, open the multiplexer dashboard described by
  the compose file's `mux:` section, so one command both brings the project up
  and shows its layout. The dashboard opens at layout 0 instead of cycling, so
  re-running `up --mux` keeps the same layout; cycling stays with
  [`cmdman compose mux up`](./cmdman-compose-mux.1.md). A project whose file has
  no `mux:` section is brought up as usual and the dashboard is skipped with a
  warning.

## Examples

```sh
cmdman compose up
cmdman compose up api
cmdman compose --project-name preview up --remove-orphan
```

## See Also

[cmdman-compose-create(1)](./cmdman-compose-create.1.md), [cmdman-compose-start(1)](./cmdman-compose-start.1.md)
