# cmdman-migrate(1)

## Name

`cmdman migrate` - apply persistent-store schema migrations

## Synopsis

```text
cmdman migrate
```

## Description

Opens the selected persistent store, applies all pending database schema
migrations, and prints `migrations complete`. Run this command against the same
data directory used by normal cmdman operations.

Migration changes persistent metadata only. It does not start, stop, or attach
to managed commands.

## Options

No command-specific options. Use global `--data-dir` to select which persistent
store is migrated.

## See Also

[cmdman(1)](./cmdman.1.md)
