# cmdman-compose-ps(1)

## Name

`cmdman compose ps` - list commands belonging to a selected project

## Synopsis

```text
cmdman compose [selection flags] ps [--format FORMAT] [COMMAND...]
```

## Description

Lists stored commands matching the selected `(workdir, project)` labels,
including exited and failed commands. Optional service names narrow the result.
The output includes compose service name, cmdman ID and generated name, state,
exit code, and argv.

This command reports stored reality; it does not create missing desired
commands or remove orphans.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `--format FORMAT`: built-in table, `json`, or a Go template.

## See Also

[cmdman-compose-ls(1)](./cmdman-compose-ls.1.md), [cmdman-compose-inspect(1)](./cmdman-compose-inspect.1.md)
