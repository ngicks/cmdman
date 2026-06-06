# cmdman-stop(1)

## Name

`cmdman stop` - gracefully stop one or more running commands

## Synopsis

```text
cmdman stop [--signal SIGNAL] [--timeout SECONDS] ID|NAME...
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

## Examples

```sh
cmdman stop api worker
cmdman stop --signal HUP --timeout 30 server
```

## See Also

[cmdman-signal(1)](./cmdman-signal.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
