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

Inspection does not require the command to be running.

## Options

- `--format FORMAT`: built-in output, `json`, or a Go template.

## See Also

[cmdman-ls(1)](./cmdman-ls.1.md), [cmdman-compose-inspect(1)](./cmdman-compose-inspect.1.md)
