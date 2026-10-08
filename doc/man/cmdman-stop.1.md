# cmdman-stop(1)

## Name

`cmdman stop` - gracefully stop one or more running commands

## Synopsis

```text
cmdman stop [--signal SIGNAL] [--timeout DURATION] [--ignore-errors] ID|NAME...
```

## Description

Sends a termination signal to each selected command's process group and waits
for shutdown. If the command does not stop before the timeout, cmdman sends
`SIGKILL`.

A command with a stop command stops in three steps. The `stop` key of
[cmdman-compose(5)](./cmdman-compose.5.md) stores a stop command.

1. The stop command runs first, for at most the timeout. It runs in the
   command's working directory and gets the command's environment with
   `CMDMAN_DATA_DIR`, `CMDMAN_RUNTIME_DIR`, `CMDMAN_CMD_DATA_DIR`, and
   `CMDMAN_CMD_ID` set for the command. `CMDMAN_MAIN_PID` holds the pid of the
   command's own process. It is empty when that process has already exited.
   cmdman discards the output of the stop command.
2. The stop command exits or the timeout runs out. cmdman then kills the
   process group of the stop command. A stop command that fails or runs out the
   timeout only produces a warning in the monitor's log.
3. cmdman sends the stop signal when the command's process or another member of
   its process group is still running. `SIGKILL` follows one more timeout later.
   cmdman sends no stop signal when nothing is left.

Such a stop waits up to twice the timeout before `SIGKILL`. The monitor carries
out every step on its own clock, so an interrupted `cmdman stop` keeps the same
schedule. A second stop while a stop is in progress changes nothing. A stop with `--signal SIGKILL` skips the
stop command, kills a stop command that is still running, and sends `SIGKILL`
at once.

The timeout is the only escalation to `SIGKILL`. It also applies to survivors of
a wrapper script that exited early. The monitor sends `SIGKILL` at the same
deadline on its own. An interrupted `cmdman stop` therefore still ends the
command. A signal to the process group cannot reach a survivor that runs in a
process group of its own inside the command's session. After the `SIGKILL` went
out, the monitor sends `SIGKILL` to each such survivor. The monitor reports the
processes still alive 10 seconds after that `SIGKILL` as `survivors_unreaped` on
the `exited` event and as a warning in the command state. On platforms other
than Linux the monitor cannot enumerate the survivors, sends nothing after the
stop's `SIGKILL`, and reports no count.
[cmdman-events(1)](./cmdman-events.1.md) describes the `survivors_unreaped`
attribute.

The whole process group is targeted, so child processes launched by a shell are
normally stopped with their parent. A stop request suppresses monitor restart
policies. Multiple targets are attempted independently; per-target failures are
written to stderr.

## Options

- `-s, --signal SIGNAL`: signal to send before waiting. When omitted, the
  command's stored stop signal is used.
- `-t, --timeout DURATION`: time to wait after the stop signal before sending
  `SIGKILL`. It also bounds the stop command of a command that has one. Give
  integer seconds or a Go duration such as `1m30s`. The value must be positive. When omitted, each command waits the stop timeout stored
  with it by `--stop-timeout` of [cmdman-create(1)](./cmdman-create.1.md) or
  `stop_grace_period` of [cmdman-compose(5)](./cmdman-compose.5.md). A command
  with no stored stop timeout waits 10 seconds.
- `--ignore-errors`: exit 0 even when some targets failed. Failures are still
  printed.

## Examples

```sh
cmdman stop api worker
cmdman stop --signal HUP --timeout 30 server
cmdman stop --timeout 1m30s db
```

## Exit Status

- `0`: cmdman handled every target. A command that had already stopped counts
  as handled.
- `1`: at least one target failed. cmdman prints one line per failure in the
  form `stop <id>: <reason>`, then the summary
  `error: one or more stop operations failed`. `--ignore-errors` turns this
  exit into `0`.

Errors that abort the whole call keep their non-zero exit under
`--ignore-errors`. An unknown target, an unparsable `--signal` value, an
unparsable or non-positive `--timeout` value, and a store that cannot be opened
are such errors. A rejected `--signal` or `--timeout` value stops no command.

## See Also

[cmdman-signal(1)](./cmdman-signal.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
