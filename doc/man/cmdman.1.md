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

## Storage And Runtime Directories

`--data-dir` selects persistent state: command definitions, the state database,
event history, and command data directories. `--runtime-dir` selects ephemeral
IPC endpoints used to communicate with live monitors. Every command that needs
to address the same cmdman installation must use the same pair.

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
[send-keys](./cmdman-send-keys.1.md), [logs](./cmdman-logs.1.md),
[events](./cmdman-events.1.md), [inspect](./cmdman-inspect.1.md),
[ls](./cmdman-ls.1.md), [mux](./cmdman-mux.1.md), [tui](./cmdman-tui.1.md).

Project management: [compose](./cmdman-compose.1.md).

File formats: [cmdman-compose(5)](./cmdman-compose.5.md),
[cmdman-mux(5)](./cmdman-mux.5.md).

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
[cmdman-mux(5)](./cmdman-mux.5.md), [cmdman-run(1)](./cmdman-run.1.md)
