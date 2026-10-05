# cmdman-compose-ps(1)

## Name

`cmdman compose ps` - list commands belonging to a selected project

## Synopsis

```text
cmdman compose [selection flags] ps [--format FORMAT] [--scale N] [COMMAND...]
```

## Description

Lists stored commands matching the selected `(workdir, project)` labels,
including exited and failed commands. Optional service names narrow the result.

The listing also includes the intermediates of the project: the cmdman commands
lifecycle hooks create for a replica. A hook run is an `exec` intermediate named
`<replica>.hook.<item>.<event>`. It exists while the hook runs and stays after a
failed run. A resource value is kept
by a `holder` intermediate named `<replica>.res.<key>`, which is never started.
Intermediates are listed after the replicas of their service. Optional service
names select an intermediate by the replica it belongs to.

Columns: `COMMAND` (the compose service name), `ID`, `NAME`, `KIND`, `OWNER`,
`STATE`, `EXIT CODE`, `STATUS`, `BELL`, `DETAIL`, `TITLE`, `ARGV`.

- `KIND` is `exec` or `holder` for an intermediate, `-` for a replica. The
  template field is `.Intermediate`, empty for a replica.
- `OWNER` is the name of the replica an intermediate belongs to, `-` for a
  replica.
- `COMMAND` is the service of that replica for an intermediate, `-` for a hook
  run whose replica is gone. Such a hook run is listed only when no service name
  is given.
- `STATUS` and `DETAIL` are what the command last reported about itself through
  [`cmdman status set`](./cmdman-status.1.md); `-` when it reported nothing.
- `BELL` is `*` when the command rang a bell nobody has looked at since, `-`
  otherwise.
- `TITLE` is the window title the command last set, truncated to 30 cells; `-`
  when it set none.
- Those four are per-run state held by the monitor, so a command that is not
  running shows `-` in all of them.

This command reports stored reality; it does not create missing desired
commands or remove orphans.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

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
- `--scale N`: list only replica N (1-based) of exactly one COMMAND. N must name
  an existing replica.

## See Also

[cmdman-compose-ls(1)](./cmdman-compose-ls.1.md), [cmdman-compose-inspect(1)](./cmdman-compose-inspect.1.md)
