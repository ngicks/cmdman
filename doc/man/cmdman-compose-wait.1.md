# cmdman-compose-wait(1)

## Name

`cmdman compose wait` - wait for project commands to reach a state

## Synopsis

```text
cmdman compose [selection flags] wait [flags] [COMMAND...]
```

## Description

Waits for selected compose services to reach `stopped`, `created`, `starting`,
`running`, `exited`, or `failed`. The default is `stopped`; the default polling
interval is 250ms. With no names, every command in the selected project is
waited on.

One outcome is printed per service. Missing services normally fail the
operation, which is useful for catching misspelled service names in automation.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `--workdir`.

## Options

- `--condition STATE`: wait condition. Defaults to `stopped`.
- `--interval DURATION`: polling interval. Defaults to `250ms`.
- `--ignore`: ignore commands that cannot be resolved.

## See Also

[cmdman-wait(1)](./cmdman-wait.1.md), [cmdman-compose-up(1)](./cmdman-compose-up.1.md)
