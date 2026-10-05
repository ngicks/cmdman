# cmdman-compose-down(1)

## Name

`cmdman compose down` - stop and remove compose-managed commands

## Synopsis

```text
cmdman compose [selection flags] down [--progress MODE] [--force] [COMMAND...]
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

Down removes every replica of a command. To remove replicas, scale the command
down with `cmdman compose scale COMMAND=N`.

Each replica runs the hooks stored on it. A running replica runs `stop_pre`,
stops, then runs `stop_post`. Every replica then runs `remove_pre`, is removed,
then runs `remove_post`. `remove_post` still reads the values of the replica's
resources. Once a replica is removed, down also removes the hook commands its
failed hooks left for inspection. These include the hook command of a failed
`remove_post`. A hook command that is still running stays.

A hook that fails under `on_error: fail` keeps its replica:

- A failed `stop_pre` or `stop_post` keeps the replica from being removed. It
  stays running after a failed `stop_pre`, and stopped after a failed
  `stop_post`.
- A failed `remove_pre` keeps the replica from being removed.
- A failed `remove_post` comes after the removal. A resource it was to release
  keeps its value, which the error names.

Stored hooks that down cannot decode also keep their replica. Down reports the
decoding error and runs no hook for that replica.

A replica whose stop fails for any other reason is removed by force. Down exits
non-zero when it keeps a replica.

With no command names, down then retries the resources a removed replica left
behind. For every resource of the project whose replica was gone before down
began, down runs the release event stored with its value, in the directory and
environment stored with it, with `CMDMAN_COMPOSE_RESOURCE_KEY` and
`CMDMAN_COMPOSE_RESOURCE_VALUE` set. The `on_error` of the release applies:

- `fail`: a failed release keeps the value, and down exits non-zero.
- `continue`: a failed release keeps the value with a warning.
- `ignore`: the value is dropped whether the release worked or not.

A release that worked drops the value. A value that has no release event, such
as one stored by `compose resource set` alone, stays. The retry needs no
compose file, so `cmdman compose -p NAME down` retries the releases of a project
whose file is gone.

When down cannot list the resources of the project, it reports the failure as a
failed release and exits non-zero. Down still reports the stop and remove
outcomes of the replicas it tore down before that.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Hook runs have records of their own;
  see [Progress Output](./cmdman-compose.5.md#progress-output).
- `--force`: treat every hook that fails under `on_error: fail` as
  `on_error: continue`. Down reports the failure as a warning and stops and
  removes the replica anyway. A replica whose stored hooks down cannot decode
  is stopped and removed without its hooks. Down reports that with a
  `hook-warning` record that names no hook. A retried release that fails keeps
  its value with a warning. `--force` has no `-f` short form: `-f` is
  `--file`.

## See Also

[cmdman-compose-stop(1)](./cmdman-compose-stop.1.md), [cmdman-compose-create(1)](./cmdman-compose-create.1.md),
[cmdman-compose-resource(1)](./cmdman-compose-resource.1.md)
