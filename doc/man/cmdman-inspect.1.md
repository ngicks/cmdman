# cmdman-inspect(1)

## Name

`cmdman inspect` - show a command's merged definition and runtime history

## Synopsis

```text
cmdman inspect [--format FORMAT] ID|NAME
```

## Description

Displays the persisted command definition together with current runtime state
and exit history. This is the authoritative way to verify the exact argv,
working directory, stored environment, labels, restart policy, monitor state,
socket path, and recorded exits used by cmdman.

The runtime state a live monitor serves carries `Cwd` beside the title, the
reported status and the bell: the working directory the command reports for
itself, from its latest OSC 7 report or from the configured directory the run
was seeded with. It answers where the command stands now rather than where its
definition says it starts, and is empty when nothing reported parsed as a path.

Inspection does not require the command to be running.

## Options

- `--format FORMAT`: built-in output, `json`, or a Go template.

## See Also

[cmdman-ls(1)](./cmdman-ls.1.md), [cmdman-compose-inspect(1)](./cmdman-compose-inspect.1.md)
