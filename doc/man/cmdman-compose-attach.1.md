# cmdman-compose-attach(1)

## Name

`cmdman compose attach` - attach to a compose service's managed PTY

## Synopsis

```text
cmdman compose [selection flags] attach [flags] SERVICE
```

## Description

Resolves `SERVICE` within the selected project and attaches to its PTY. The
service command must have `tty: true`.

Attach is sticky by default: after an exit it remains available for later
restarts and can request a restart through the sticky attach interface.

Detaching affects only the client; it does not stop the compose service.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--no-stdin`: receive output only; do not forward local stdin to the PTY.
- `--sig-proxy`: forward local signals to the remote command. Defaults to true.
- `--detach-keys KEYS`: detach key sequence, default `ctrl-p,ctrl-q`.
- `--auto-exit`: exit when the command exits or is not running instead of using
  sticky attach behavior.

## See Also

[cmdman-attach(1)](./cmdman-attach.1.md), [cmdman-compose-send-keys(1)](./cmdman-compose-send-keys.1.md)
