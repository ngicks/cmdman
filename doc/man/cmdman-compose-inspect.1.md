# cmdman-compose-inspect(1)

## Name

`cmdman compose inspect` - inspect stored commands in a compose project

## Synopsis

```text
cmdman compose [selection flags] inspect [--format FORMAT] [COMMAND...]
```

## Description

Resolves service names within the selected project and returns the same merged
definition, runtime state, labels, and exit history available from `cmdman
inspect`. With no service names, every stored command in the project is
inspected.

This operation reads stored command state. It does not imply that the current
compose file still declares every returned command, so it can expose orphans.
Use `compose config` to inspect current desired configuration.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--format FORMAT`: built-in output, `json`, or a Go template.

## See Also

[cmdman-inspect(1)](./cmdman-inspect.1.md), [cmdman-compose-config(1)](./cmdman-compose-config.1.md)
