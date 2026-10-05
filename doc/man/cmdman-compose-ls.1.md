# cmdman-compose-ls(1)

## Name

`cmdman compose ls` - list projects known from stored command labels

## Synopsis

```text
cmdman compose ls [--format FORMAT]
```

## Description

Lists every compose project known to the selected cmdman data directory. This
command does not discover files from disk; projects appear because commands
created by compose carry compose labels.

Projects are grouped by both work directory and project name. The summary
includes source compose file, command count, and running, exited, and failed
counts.

`INTERMEDIATES` (template field `.Intermediates`) counts the cmdman commands
lifecycle hooks created for the replicas of the project: kept hook runs and
resource holders. The command and state counts leave them out. A project whose
replicas are all removed stays listed while it has intermediates.

## Options

- `--format FORMAT`: built-in table, `json`, or a Go template.

## See Also

[cmdman-compose-ps(1)](./cmdman-compose-ps.1.md), [cmdman-compose(1)](./cmdman-compose.1.md)
