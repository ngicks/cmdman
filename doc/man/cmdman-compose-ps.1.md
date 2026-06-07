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

Unlike the lifecycle subcommands, `ps` does not auto-discover a
`cmd-compose.yaml` in the working directory to narrow the listing: with neither
`-f` nor `-p` it lists **every** command in the working directory, across all
co-located projects (for example a `cmd-compose.yaml` project plus named
projects whose `work_dir` points here). This keeps a status listing from
silently hiding co-located projects. Pass `-f FILE` or `-p NAME` to scope the
listing to a single project, or use [`cmdman compose ls`](./cmdman-compose-ls.1.md)
for a project summary across the whole data directory.

## Options

- `--format FORMAT`: built-in table, `json`, or a Go template.

## See Also

[cmdman-compose-ls(1)](./cmdman-compose-ls.1.md), [cmdman-compose-inspect(1)](./cmdman-compose-inspect.1.md)
