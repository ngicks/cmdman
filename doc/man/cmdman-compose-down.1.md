# cmdman-compose-down(1)

## Name

`cmdman compose down` - stop and remove compose-managed commands

## Synopsis

```text
cmdman compose [selection flags] down [--progress MODE] [--force] [--timeout DURATION]
    [COMMAND...]
```

## Description

Stops selected commands, waits for the stop phase, then removes their stored
records concurrently. It is the destructive counterpart to `compose stop`. Each
stop uses the stored stop command, stop signal, and stop timeout of its
replica. `--timeout` overrides the stop timeout.

Down stops at most `--parallel` replicas at once. The default is 4. The limit
covers the declared commands and the orphans of a whole-project teardown. It
counts the stops of one down. Downs of several projects may run at once. Each
of them stops up to the limit. The limit does not apply to removal.

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

A replica that the `SIGKILL` after the timeout ends counts as force-killed.
Down reads that from the stop before it removes the replica. The progress
output marks it on the replica's `stopped` record. In `json` mode that record
sets `forceKilled` to `true`. In `tty` mode a warning line follows the
replica's `Stopped` line:

```text
! force-killed after the grace period; detached processes may survive
```

A forced kill does not fail the down.

A `stop_pre` or `stop_post` release can fail because its replica has not fully
gone down yet. Down therefore runs each release that failed in the stop hooks
once more. The retry comes after every stop and before any removal. It runs
only when the replica has verifiably stopped: its state is `exited`, or
`failed` with the end of its run recorded by its monitor. A replica whose
monitor died does not count as stopped, and neither does a replica that still
runs or a replica that is gone. Such a replica may still use the resource, so
its release does not run again. A release that works on the retry drops the
value, and down reports no failure for it. A release that fails again reports
the failure of the retry. The retry runs no other hook. A replica that a failed
stop hook keeps stays kept, and down still exits non-zero.

With no command names, down then releases the resources that removed replicas
left behind. Down runs the release event stored with the value of these
resources:

- Every resource of the project whose replica was gone before down began.
- Every resource with a `stop_pre` or `stop_post` release whose replica down
  removed without stopping it. Down stops only a starting or running replica.
  A replica whose command exited on its own therefore runs no stop hooks.

Down runs these releases after the remove hooks of every replica. Each release
runs in the directory and environment stored with the value, with
`CMDMAN_COMPOSE_RESOURCE_KEY` and `CMDMAN_COMPOSE_RESOURCE_VALUE` set.

A release that worked drops the value. A `remove_post` release and each of
these stored releases run after the retry, and down runs each of them at most
once. The next down retries a release that failed in the hooks of a replica. A value that has no release event, such as one stored by
`compose resource set` alone, stays. These releases need no compose file, so
`cmdman compose -p NAME down` retries the releases of a project whose file is
gone.

Down lists every resource whose release failed as unreleased, whatever
`on_error` the release ran under. A resource whose release worked on the retry
is not listed. A release that failed again on its retry reports the error of
the retry. Each unreleased resource gets a record of its own in the progress
output, with its key, its value, and the error. See
[Progress Output](./cmdman-compose.5.md#progress-output). The `on_error` of the
release decides the exit status and what happens to the value:

- `fail`: a failed release keeps the value, and down exits non-zero.
- `continue`: a failed release keeps the value with a warning.
- `ignore`: the value is dropped whether the release worked or not.

When down cannot list the resources of the project, it reports the failure as a
failed release and exits non-zero. Down still reports the stop and remove
outcomes of the replicas it tore down before that.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Hook runs and unreleased resources
  have records of their own; see
  [Progress Output](./cmdman-compose.5.md#progress-output).
- `--force`: treat every hook that fails under `on_error: fail` as
  `on_error: continue`. Down reports the failure as a warning and stops and
  removes the replica anyway. A replica whose stored hooks down cannot decode
  is stopped and removed without its hooks. Down reports that with a
  `hook-warning` record that names no hook. A stored release that fails keeps
  its value with a warning. `--force` has no `-f` short form: `-f` is
  `--file`.
- `-t, --timeout DURATION`: time each replica's stop waits after the stop
  signal before it sends `SIGKILL`. Give integer seconds or a Go duration such
  as `1m30s`. The value must be positive. When omitted, each replica waits its
  stored `stop_grace_period`, or 10 seconds when the command declares none. A
  replica with a `stop` command runs it first for at most the same time, so its
  stop can take up to twice the timeout before `SIGKILL`. A replica whose stop
  failed is removed by force, and that removal waits the stored value. A
  rejected value stops and removes no replica and runs no hook.
- `--parallel N`: stop at most N replicas at once, or every replica at once
  with `-1`. `CMDMAN_COMPOSE_PARALLEL_LIMIT` sets the limit when the flag is not
  given. See [cmdman-compose(1)](./cmdman-compose.1.md#options).

## See Also

[cmdman-compose-stop(1)](./cmdman-compose-stop.1.md), [cmdman-compose-create(1)](./cmdman-compose-create.1.md),
[cmdman-compose-resource(1)](./cmdman-compose-resource.1.md)
