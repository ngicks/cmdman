# cmdman-create(1)

## Name

`cmdman create` - persist a command definition without starting it

## Synopsis

```text
cmdman create [flags] -- COMMAND [ARGS...]
```

## Description

Creates a command record in `created` state and prints its name, or its ID when
no name was supplied. No monitor or child process is started. Use `cmdman
start` later to launch the stored definition.

`COMMAND` and each argument are stored as an argv array and executed directly,
without shell parsing. Use an explicit shell such as `sh -c '...'` when shell
syntax is required.

The working directory, environment, restart policy, stop signal, TTY choice,
scrollback limit, log driver, labels, and argv are persisted.

## Options

- `-n, --name NAME`: assign a unique human-readable target name.
- `-w, --workdir DIR`: child working directory.
- `-E, --env KEY=VALUE`: stored environment entry; repeatable. Entries are
  layered on top of the imported host environment and override it.
- `--import-host-env`: import cmdman's creation-time environment as the base;
  default true. `--import-host-env=false` starts the command from an empty
  environment plus the `--env` entries, so include `PATH`, `HOME`, and similar
  variables when the command needs them.
- `--inject-env`: put `CMDMAN_DATA_DIR`, `CMDMAN_RUNTIME_DIR`,
  `CMDMAN_CMD_DATA_DIR`, and `CMDMAN_CMD_ID` into the command's environment;
  default true. cmdman replaces inherited entries of those names with this
  command's own values. `--inject-env=false` leaves the inherited entries
  untouched and adds none of the four. Use it when the command runs its own
  cmdman that must keep addressing an outer cmdman's command. The command's
  hooks always receive the four variables of the command they belong to.
- `-l, --label KEY=VALUE`: metadata used by `ls --label` and `rm --label`.
- `--restart no|on-failure[:N]|always`: monitor restart policy.
- `--stop-signal SIGNAL`: default signal used by `stop`.
- `-t, --tty`: allocate a PTY; required for `attach` and `send-keys`.
- `--rm`: remove the command record after its terminal exit.
- `--scrollback-bytes N`: in-memory output replay limit for attaching clients.
- `--log-driver k8s-file|none` and `--log-opt KEY=VALUE`: persistent logging.

## Examples

```sh
cmdman create --name worker --restart on-failure:5 -- ./worker
cmdman create --name shell --tty --env TERM=xterm-256color -- /bin/zsh
cmdman create --name inner --inject-env=false -- ./entrypoint
cmdman start worker
```

## See Also

[cmdman-run(1)](./cmdman-run.1.md), [cmdman-start(1)](./cmdman-start.1.md)
