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

The attached client also reports the command's working directory to the terminal
it runs in, by two routes. It moves itself into the command's configured working
directory before the stream starts, so a multiplexer that reads a pane's path
off the process standing in it — tmux's `#{pane_current_path}` — names the
command's directory rather than the one the attach was typed in; a directory
that is gone or cannot be entered costs only that, never the attach. The replay
then carries the working directory the command last reported for itself through
an OSC 7 sequence, re-emitted before live output continues and seeded at run
start from the configured directory, so `#{pane_path}` is right on a fresh
attach even when scrollback has long rotated the original report away, and
follows a `cd` made inside a supervised shell.

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

[cmdman-send-keys(1)](./cmdman-send-keys.1.md), [cmdman-logs(1)](./cmdman-logs.1.md), [cmdman-capture-screen(1)](./cmdman-capture-screen.1.md)
