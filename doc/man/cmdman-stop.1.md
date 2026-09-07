# cmdman-stop(1)

## Name

`cmdman stop` - gracefully stop one or more running commands

## Synopsis

```text
cmdman stop [--signal SIGNAL] [--timeout SECONDS] [--ignore-errors] ID|NAME...
```

## Description

Sends a termination signal to each selected command's process group and waits
for shutdown. If the command does not stop before the timeout, cmdman sends
`SIGKILL`.

The whole process group is targeted, so child processes launched by a shell are
normally stopped with their parent. A stop request suppresses monitor restart
policies. Multiple targets are attempted independently; per-target failures are
written to stderr.

## Options

- `-s, --signal SIGNAL`: signal to send before waiting. When omitted, the
  command's stored stop signal is used.
- `-t, --timeout SECONDS`: seconds to wait before sending `SIGKILL`. Defaults
  to 10.
- `--ignore-errors`: exit 0 even when some targets failed. Failures are still
  printed.

## Examples

```sh
cmdman stop api worker
cmdman stop --signal HUP --timeout 30 server
```

## Exit Status

- `0`: cmdman handled every target. A command that had already stopped counts
  as handled.
- `1`: at least one target failed. cmdman prints one line per failure in the
  form `stop <id>: <reason>`, then the summary
  `error: one or more stop operations failed`. `--ignore-errors` turns this
  exit into `0`.

Errors that abort the whole call keep their non-zero exit under
`--ignore-errors`. An unknown target, an unparsable `--signal` value, and a
store that cannot be opened are such errors.

## See Also

[cmdman-signal(1)](./cmdman-signal.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
