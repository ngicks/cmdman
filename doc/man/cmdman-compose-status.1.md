# cmdman-compose-status(1)

## Name

`cmdman compose status` - show what a project's commands report about themselves

## Synopsis

```text
cmdman compose [selection flags] status [--format FORMAT] [COMMAND...]
```

## Description

The project-wide read of [`cmdman status get`](./cmdman-status.1.md): the status
and detail every command in the selected project last reported about itself,
plus the window title it last set and whether its bell is still unread. Optional
service names narrow the result.

Columns: `COMMAND`, `STATE`, `STATUS`, `BELL`, `DETAIL`, `TITLE`.

- `STATUS` is `working`, `waiting`, or `done`, and `DETAIL` its free-form
  companion; `-` when the command reported nothing.
- `BELL` is `*` when the command rang a bell nobody has looked at since, `-`
  otherwise.
- `TITLE` is the window title the command last set, `-` when it set none.

All of these are per-run state held by each command's monitor, so a command that
is not running has nothing to report and shows `-`. Monitors are dialled in
parallel; one that does not answer costs its own command's columns, not the
listing.

Writing a status is per command - see
[`cmdman status set`](./cmdman-status.1.md).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

Selection matches [`cmdman compose ps`](./cmdman-compose-ps.1.md): with neither
`-f` nor `-p` no `cmd-compose.yaml` is auto-discovered to narrow the listing, so
every command in the working directory is listed across all co-located projects.

## Options

- `--format FORMAT`: built-in table, `json`, or a Go template.

## Examples

```sh
cmdman compose status
cmdman compose status api worker
cmdman compose status --format json
```

## See Also

[cmdman-status(1)](./cmdman-status.1.md),
[cmdman-compose-ps(1)](./cmdman-compose-ps.1.md),
[cmdman-compose(1)](./cmdman-compose.1.md)
