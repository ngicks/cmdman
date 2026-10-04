# cmdman-compose-start(1)

## Name

`cmdman compose start` - start existing project commands in dependency order

## Synopsis

```text
cmdman compose [selection flags] start [--progress MODE] [--scale N] [COMMAND...]
```

## Description

Starts previously created project commands without reconciling changed compose
definitions. Use `compose up` when the file may have changed.

Named targets pull in recursive dependencies and start according to
`after.condition`; independent commands run concurrently. When no compose file
is loaded, dependency order is reconstructed from stored compose labels.

Already starting or running commands are treated as successful. Failures are
aggregated rather than immediately cancelling unrelated starts.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise.
- `--scale N`: start only replica N (1-based) of exactly one COMMAND. N must name
  an existing replica. Its dependencies are still started in full.

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-compose-stop(1)](./cmdman-compose-stop.1.md)
