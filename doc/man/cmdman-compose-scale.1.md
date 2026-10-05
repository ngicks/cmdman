# cmdman-compose-scale(1)

## Name

`cmdman compose scale` - set the replica count of compose commands

## Synopsis

```text
cmdman compose [selection flags] scale [--progress MODE] COMMAND=NUM [COMMAND=NUM...]
```

## Description

Sets the replica count of each named command and reconciles the project to it.
Scale creates and starts the missing replicas, and it stops and removes the
surplus replicas. NUM must be 1 or greater, and every COMMAND must be declared
in the compose file. Commands left out keep their `scale` from the file.

Each replica is a cmdman command of its own, numbered 1 to NUM. The count
applies to this operation only. Scale does not write it back to the compose
file, and a later `compose up` returns to the `scale` of the file.

Scale reconciles like [`compose up`](./cmdman-compose-up.1.md) limited to the
named commands and their `after` dependencies. It runs their hooks the same
way. A new replica runs its create hooks and start hooks. A surplus replica
runs the stop hooks stored on it when it is starting or running, and then its
stored remove hooks. A surplus replica that is neither starting nor running
runs no stop hooks. Once scale removes such a replica, it runs the stored
`stop_pre` or `stop_post` release of each resource of the replica, as
[`compose down`](./cmdman-compose-down.1.md) does. A hook that fails under
`on_error: fail` ends the action of its replica at that hook and makes scale
exit non-zero. A release that fails under
`on_error: fail` or `on_error: continue` keeps its value. Under
`on_error: fail`, it also makes scale exit non-zero. See
[Lifecycle Hooks](./cmdman-compose.5.md#lifecycle-hooks).

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`. Scale always loads a compose file.

## Options

- `--progress auto|tty|json|quiet`: progress output mode. `auto` chooses TTY
  output on terminals and JSON otherwise. Hook runs have records of their own;
  see [Progress Output](./cmdman-compose.5.md#progress-output).

## Examples

```sh
cmdman compose scale web=3
cmdman compose scale web=1 worker=2
```

## See Also

[cmdman-compose-up(1)](./cmdman-compose-up.1.md), [cmdman-compose(5)](./cmdman-compose.5.md)
