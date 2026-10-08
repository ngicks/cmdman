# cmdman(1)

## Name

`cmdman` - run commands under detached monitors and control them later

## Synopsis

```text
cmdman [global flags] COMMAND [arguments...]
cmdman --version
```

## Description

cmdman stores a command definition, starts a detached monitor for it, and
exposes later lifecycle operations through the CLI. The monitor owns the child
process, captures output, tracks state and exit history, and optionally exposes
a PTY for interactive commands. Closing the terminal that invoked cmdman does
not stop the managed command.

A command can be addressed by its generated ID or by a unique name supplied at
creation time. Compose-managed commands additionally carry project, work
directory, compose-file, and service-name labels.

## State Model

The principal states are `created`, `starting`, `running`, `exited`, and
`failed`. `create` stops at `created`; `start` launches its monitor. A normal
process exit becomes `exited`, even when its exit code is non-zero. Failures to
launch or supervise the process become `failed`.

Restart policies are enforced by the detached monitor. Explicit `stop` requests
do not trigger policy-based restart.

When the command's own process exits, the monitor deals with the processes the
command left in its own session before it changes the state. The monitor treats
them differently after a natural exit and after a stop. In both cases it leaves
alone a process that made a session of its own. A program that deliberately
detached to outlive the run, such as a shared terminal-multiplexer server, is
never swept, and a restart does not tear it down. The sweep does not affect
hooks the monitor runs.

After a crash or a normal exit, the monitor runs a full sweep. On Linux the
monitor reaps the leftovers, sends `SIGTERM` to each one by PID, waits
2 seconds, sends `SIGKILL`, reaps again, and rescans until the session is empty.
The sweep gives up after 10 seconds. It signals individual PIDs only and never
signals the process group. Other platforms offer the monitor no subreaper and no
`/proc`. The monitor cannot enumerate the leftovers there. On those platforms
the sweep probes the command's process group, sends it `SIGTERM` once when the
probe finds a member, and sends it `SIGKILL` after the 2 second grace period. A
restart therefore never stacks a run's in-session leftovers on top of the new
run.

After a stop, the sweep sends no signal of its own on any platform. A stop comes
from `cmdman stop`, `cmdman compose stop`, `cmdman compose down`, or the TUI.
The stop already delivered the configured stop signal to the whole process
group once. A leftover of a wrapper shell that exited first is usually the
process that carries out the stop. Podman waiting for its container is one
example. The monitor only reaps the leftovers and waits for them. `SIGKILL`
comes from the stop's timeout alone. The client sends `SIGKILL` when the
timeout expires, and the monitor escalates at the same deadline on its own. An
interrupted `cmdman stop` therefore still ends the command. A signal to the
process group cannot reach a survivor that runs in a process group of its own
inside the command's session. After the `SIGKILL` went out, the monitor sends
`SIGKILL` to each such survivor. The monitor reports the processes still alive
10 seconds after that `SIGKILL` as `survivors_unreaped` on the `exited` event
and as a warning in the command state. On platforms other than Linux the monitor
cannot enumerate the survivors, sends nothing after the stop's `SIGKILL`, and
reports no count. A stop that arrives while a natural-exit sweep is in progress
ends that sweep's own signalling at once.

The stop's timeout is the single setting for how long a stop waits before
`SIGKILL`. The stop's `--timeout` sets it. Without `--timeout`, the stop waits
the stop timeout stored with the command, or 10 seconds when it has none. The
timeout also covers the survivors of a wrapper that exited early. A stop
adds no grace period of its own. A wrapper script does not need `exec` to stop
correctly. Using `exec` in a wrapper script remains good hygiene.

## Reported Status

Separate from the state model above, a command can report about itself: one of
`working`, `waiting`, or `done`, plus an optional free-form detail. That is the
command's own word, not cmdman's observation of it.

cmdman puts `CMDMAN_CMD_ID` into the environment of every command it supervises,
so a supervised command addresses itself by passing no argument at all; from
outside, name the command by ID or name. Creating a command with
`--inject-env=false` suppresses that injection, so the command keeps the
`CMDMAN_CMD_ID` value of an outer cmdman. Reads and writes reach the command's
monitor over its per-command Unix socket, so the status is per-run state: it is
cleared when the command restarts and gone once it exits. Writing requires a
running monitor and fails without one; reading a command that has none reports
nothing rather than failing.

## Storage And Runtime Directories

`--data-dir` selects persistent state: command definitions, the state database,
and command data directories. `--runtime-dir` selects ephemeral state: the IPC
endpoints used to communicate with live monitors, and the event log. Every
command that needs to address the same cmdman installation must use the same
pair.

The event log is cleared with the runtime dir, e.g. on reboot, so
`events --since` cannot reach history recorded before it was cleared.

Commands persist the environment captured at creation time. Supplying one or
more explicit environment entries replaces the default inherited environment;
it is not an overlay applied at process start.

## Global Options

- `--data-dir DIR`: use an alternate persistent data directory.
- `--runtime-dir DIR`: use an alternate runtime/IPC directory.
- `--log[=text|json]`: enable cmdman diagnostic logging.
- `--log-level LEVEL`: set diagnostic verbosity.
- `--version`: alias for `cmdman version`.

## Commands

Lifecycle: [create](./cmdman-create.1.md), [run](./cmdman-run.1.md),
[start](./cmdman-start.1.md), [stop](./cmdman-stop.1.md),
[restart](./cmdman-restart.1.md), [rm](./cmdman-rm.1.md),
[wait](./cmdman-wait.1.md), [signal](./cmdman-signal.1.md).

Interaction and observation: [attach](./cmdman-attach.1.md),
[send-keys](./cmdman-send-keys.1.md),
[capture-screen](./cmdman-capture-screen.1.md), [logs](./cmdman-logs.1.md),
[events](./cmdman-events.1.md), [inspect](./cmdman-inspect.1.md),
[ls](./cmdman-ls.1.md), [status](./cmdman-status.1.md) (subcommands: `set`,
`get`, `delete`), [mux](./cmdman-mux.1.md) (subcommands: `up`, `down`, `ls`,
`frame`),
[tui](./cmdman-tui.1.md) (subcommand group: `widget`).

Project management: [compose](./cmdman-compose.1.md).

File formats: [cmdman-compose(5)](./cmdman-compose.5.md),
[cmdman-mux(5)](./cmdman-mux.5.md), [cmdman-frame(5)](./cmdman-frame.5.md).

Maintenance: [migrate](./cmdman-migrate.1.md), [help](./cmdman-help.1.md),
[version](./cmdman-version.1.md).

## Examples

```sh
cmdman run --name server --restart always -- ./server --listen :8080
cmdman logs --follow server
cmdman stop server
cmdman start server
```

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose(5)](./cmdman-compose.5.md),
[cmdman-mux(5)](./cmdman-mux.5.md), [cmdman-frame(5)](./cmdman-frame.5.md),
[cmdman-run(1)](./cmdman-run.1.md)
