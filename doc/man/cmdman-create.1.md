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
- `-C, --dir DIR`: child working directory.
- `-E, --env KEY=VALUE`: stored environment entry; repeatable. An empty env
  set inherits cmdman's complete creation-time environment. Once any explicit
  env entry is provided, only those entries are stored, so include `PATH`,
  `HOME`, and similar variables when the command needs them.
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
cmdman create --name shell --tty --env PATH="$PATH" -- /bin/zsh
cmdman start worker
```

## See Also

[cmdman-run(1)](./cmdman-run.1.md), [cmdman-start(1)](./cmdman-start.1.md)
