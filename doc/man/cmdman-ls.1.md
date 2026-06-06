# cmdman-ls(1)

## Name

`cmdman ls` - list stored commands

## Synopsis

```text
cmdman ls [--all] [--quiet] [--label KEY=VALUE] [--format FORMAT]
```

## Description

Lists commands from the selected data directory. The default view omits exited
commands. Repeatable label filters are combined to narrow the result.

JSON or templates should be preferred over parsing the human-oriented table.

## Options

- `-l, --label KEY=VALUE`: filter by label. Repeatable.
- `-a, --all`: include exited and otherwise non-running commands.
- `-q, --quiet`: print only command IDs.
- `--format FORMAT`: built-in table, `json`, or a Go template.

## Examples

```sh
cmdman ls
cmdman ls --all --label role=worker
cmdman ls --quiet --label cmdman.compose.project=myproject
```

## See Also

[cmdman-inspect(1)](./cmdman-inspect.1.md), [cmdman-compose-ls(1)](./cmdman-compose-ls.1.md)
