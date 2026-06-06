# cmdman-attach(1)

## Name

`cmdman attach` - interact with a managed command's PTY

## Synopsis

```text
cmdman attach [flags] ID|NAME
```

## Description

Connects the local terminal to a running command that has a PTY. Existing
scrollback and terminal input modes are replayed before live output continues.

Attach is sticky by default: when the remote command exits, the client remains
open, reports the transition, and can reconnect after a restart.

The default detach sequence is `Ctrl-P`, then `Ctrl-Q`. Detaching closes only
the client connection; it does not stop the command.

## Options

- `--no-stdin`: receive output only; do not forward local stdin to the PTY.
- `--sig-proxy`: forward local signals to the remote command. Defaults to true.
- `--detach-keys KEYS`: detach key sequence, default `ctrl-p,ctrl-q`.
- `--auto-exit`: exit when the command exits or is not running instead of using
  sticky attach behavior.

## Examples

```sh
cmdman attach shell
cmdman attach --no-stdin server
cmdman attach --detach-keys ctrl-a,ctrl-d shell
```

## See Also

[cmdman-send-keys(1)](./cmdman-send-keys.1.md), [cmdman-logs(1)](./cmdman-logs.1.md)
