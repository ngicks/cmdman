# cmdman-compose-restart(1)

## Name

`cmdman compose restart` - stop and restart stored project commands

## Synopsis

```text
cmdman compose [selection flags] restart [--progress MODE] [--scale N] [--timeout DURATION]
    [COMMAND...]
```

## Description

Restarts the selected existing commands using their stored definitions. It does
not reconcile changes from the compose file; use `compose up` for that.

The stop phase follows reverse dependency order and the start phase follows
forward dependency order; work within each layer is concurrent. Orphans are
skipped. When no compose file is loaded, dependency order is reconstructed from
stored compose labels.

Each stop uses the stored stop command, stop signal, and stop timeout of its
replica. `--timeout` overrides the stop timeout.

The stop phase stops at most `--parallel` replicas at once. The default is 4.
The limit counts the stops of one invocation. Several invocations may run at
once. Each of them stops up to the limit. The limit does not apply to the
start phase.

The operation reports outcomes per replica. It writes its progress output
first and then one result line per replica it restarted. A replica of a scaled
command is labeled `<command>-<index>`, and the replica of an unscaled command
is labeled by the command name. Target selection is project-scoped, so service
names resolve only within the selected `(workdir, project)` pair.

Restart stops only the replicas that are starting or running. Each of these
runs the `stop_pre` and `stop_post` hooks stored on it around its stop. Every
replica, including one that never started, then runs `start_pre` and
`start_post` around its start. The start hooks come from the compose file when
one is loaded and from the copy stored on the replica otherwise. A replica
whose stop hook fails under `on_error: fail` is not started. A start hook that
fails under `on_error: fail` fails the start of its command. See
[Lifecycle Hooks](./cmdman-compose.5.md#lifecycle-hooks).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Restart reports its hook runs there;
  see [Progress Output](./cmdman-compose.5.md#progress-output).
- `--scale N`: restart only replica N (1-based) of exactly one COMMAND. N must
  name an existing replica.
- `-t, --timeout DURATION`: time each replica's stop waits after the stop
  signal before it sends `SIGKILL`. Give integer seconds or a Go duration such
  as `1m30s`. The value must be positive. When omitted, each replica waits its
  stored `stop_grace_period`, or 10 seconds when the command declares none. A
  replica with a `stop` command runs it first for at most the same time, so its
  stop can take up to twice the timeout before `SIGKILL`. A rejected value
  restarts no replica and runs no hook.
- `--parallel N`: stop at most N replicas at once, or every replica at once
  with `-1`. `CMDMAN_COMPOSE_PARALLEL_LIMIT` sets the limit when the flag is not
  given. See [cmdman-compose(1)](./cmdman-compose.1.md#options).

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
