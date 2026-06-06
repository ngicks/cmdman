# cmdman-help(1)

## Name

`cmdman help` - show command-line help for a command path

## Synopsis

```text
cmdman help [COMMAND...]
cmdman COMMAND --help
```

## Description

Prints Cobra-generated usage, available child commands, and flags for the
selected command path.

CLI help is the authoritative concise reference for flags supported by the
installed binary. The files in `doc/man` provide the behavioral details,
selection rules, lifecycle implications, and examples intentionally omitted
from terse command-line help.

## Options

- `-h, --help`: available on every command; prints help for that command path.
  `cmdman help COMMAND...` is the equivalent subcommand form.

## See Also

[cmdman(1)](./cmdman.1.md), [cmdman-compose(1)](./cmdman-compose.1.md)
