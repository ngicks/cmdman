# cmdman-compose-restart(1)

## Name

`cmdman compose restart` - stop and restart stored project commands

## Synopsis

```text
cmdman compose [selection flags] restart [--progress MODE] [--scale N] [COMMAND...]
```

## Description

Restarts the selected existing commands using their stored definitions. It does
not reconcile changes from the compose file; use `compose up` for that.

The stop phase follows reverse dependency order and the start phase follows
forward dependency order; work within each layer is concurrent. Orphans are
skipped. When no compose file is loaded, dependency order is reconstructed from
stored compose labels.

The operation reports outcomes per replica. It writes its progress output
first and then one result line per replica it restarted. A replica of a scaled
command is labeled `<command>-<index>`, and the replica of an unscaled command
is labeled by the command name. Target selection is project-scoped, so service
names resolve only within the selected `(workdir, project)` pair.

Each starting or running replica runs the `stop_pre` and `stop_post` hooks
stored on it around its stop. Every replica then runs `start_pre` and
`start_post` around its start. The start hooks come from the compose file when
one is loaded and from the copy stored on the replica otherwise. A replica
whose stop hook fails under `on_error: fail` is not started. A start hook that
fails under `on_error: fail` fails the start of its command. See
[Lifecycle Hooks](./cmdman-compose.5.md#lifecycle-hooks).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Restart reports its hook runs there;
  see [Progress Output](./cmdman-compose.5.md#progress-output).
- `--scale N`: restart only replica N (1-based) of exactly one COMMAND. N must
  name an existing replica.

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-restart(1)](./cmdman-restart.1.md)
