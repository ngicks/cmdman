# cmdman-run(1)

## Name

`cmdman run` - create and start a managed command

## Synopsis

```text
cmdman run [create flags] [--attach] -- COMMAND [ARGS...]
```

## Description

Performs `cmdman create` followed by `cmdman start`. By default, it prints the
command name or generated ID after the monitor reaches the running state. The
managed process continues independently after `cmdman run` exits.

Attaching after start is only possible when the running command exposes a live
PTY. The default detach sequence is `Ctrl-P`, `Ctrl-Q`.

All creation flags have the same persistence and environment semantics as
`cmdman create`.

## Options

`cmdman run` accepts every option documented by
[`cmdman create`](./cmdman-create.1.md), plus:

- `--attach`: attach after the command reaches running. This only opens an
  attach session when the command has a PTY, so pair it with `--tty` for
  interactive commands.

## Examples

```sh
cmdman run --name api --restart always -- ./api
cmdman run --name repl --tty --attach -- python
```

## See Also

[cmdman-create(1)](./cmdman-create.1.md), [cmdman-attach(1)](./cmdman-attach.1.md)
