# cmdman-compose-stop(1)

## Name

`cmdman compose stop` - stop project commands without removing them

## Synopsis

```text
cmdman compose [selection flags] stop [--progress MODE] [--scale N] [--timeout DURATION]
    [COMMAND...]
```

## Description

Gracefully stops selected running commands using each stored command's stop
signal and stop timeout. `--timeout` overrides the stop timeout. Stored
definitions remain available for a later `compose start`.

Naming a command also selects all recursive dependents, and stopping proceeds
in reverse dependency order. With no names, all declared project commands are
stopped. Orphans are not part of the declared graph and are not stopped by this
operation. When no compose file is loaded, dependency order is reconstructed
from stored compose labels.

Failures are aggregated rather than cancelling the remaining stops. cmdman
reports a command it could not stop as an error and exits non-zero with the
summary `error: <n> compose stop operation(s) failed`.

Each starting or running replica runs the `stop_pre` and `stop_post` hooks
stored on it around its stop. Stop needs no compose file to run them. A replica
that is already stopped runs no hooks. A hook that fails under
`on_error: fail` fails the stop of its command. The replica keeps running after
a failed `stop_pre` and stays stopped after a failed `stop_post`. See
[Lifecycle Hooks](./cmdman-compose.5.md#lifecycle-hooks).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Hook runs have records of their own;
  see [Progress Output](./cmdman-compose.5.md#progress-output).
- `--scale N`: stop only replica N (1-based) of exactly one COMMAND. N must name
  an existing replica. Its recursive dependents are still stopped in full.
- `-t, --timeout DURATION`: time each replica's stop waits after the stop
  signal before it sends `SIGKILL`. Give integer seconds or a Go duration such
  as `1m30s`. The value must be positive. When omitted, each replica waits its
  stored `stop_grace_period`, or 10 seconds when the command declares none. A
  rejected value stops no replica and runs no hook.

## See Also

[cmdman-compose-start(1)](./cmdman-compose-start.1.md), [cmdman-compose-down(1)](./cmdman-compose-down.1.md)
