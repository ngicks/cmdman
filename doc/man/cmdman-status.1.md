# cmdman-status(1)

## Name

`cmdman status` - read and write the status a command reports about itself

## Synopsis

```text
cmdman status set working|waiting|done [ID|NAME] [--detail TEXT]
cmdman status get [ID|NAME] [--format FORMAT]
cmdman status delete [ID|NAME]
```

## Description

A reported status is the command's own word about what it is doing, distinct
from the cmdman state model (`created`, `running`, `exited`, ...) which is
cmdman's observation of the process. It is one of `working`, `waiting`, or
`done`, plus an optional free-form detail. Case and surrounding space are
forgiven on input; anything outside that vocabulary is an error.

The status is held by the monitor of the current run and reaches it over the
command's per-command Unix socket. It is therefore per-run state: restarting the
command clears it, and it is gone once the command exits.

`ID|NAME` is optional because cmdman puts `CMDMAN_CMD_ID` into the environment
of every command it supervises, so a supervised command reports about itself
with no argument at all. Outside such a command, and with `CMDMAN_CMD_ID` unset,
the argument is required.

The project-wide read is [`cmdman compose status`](./cmdman-compose-status.1.md);
the `STATUS`, `BELL`, `DETAIL`, and `TITLE` columns of
[`cmdman ls`](./cmdman-ls.1.md) and
[`cmdman compose ps`](./cmdman-compose-ps.1.md) show the same values.

## Subcommands

### set

Set the status, replacing any previous one. `--detail` replaces the previous
detail, so omitting it on a later `set` clears it.

The command must be running: a status nobody can hold is a status that was lost,
so a command with no live monitor is an error rather than a silent no-op.

### get

Print the status. The default output is the status word alone, or the word and
its detail separated by a tab. A command that reported nothing prints nothing at
all, so empty output is itself the answer to "did it report".

Unlike `set` and `delete`, a command with no live monitor is not an error — one
that never started, has exited, or died mid-call reports nothing. Only an
unresolvable `ID|NAME` fails.

### delete

Clear the status. Like `set`, it needs a running monitor to talk to.

## Options

### set

- `--detail TEXT`: free-form detail shown beside the status. Omitting it on a
  later `set` clears the previous detail.

### get

- `--format FORMAT`: output format. Default plain text (above), `json`, or a Go
  `text/template` string. Template fields: `.Status`, `.Detail` (both strings,
  omitted from JSON when empty).

## Examples

```sh
# From inside a supervised command - no argument needed
cmdman status set waiting --detail "needs input"
cmdman status set done
cmdman status delete

# From outside, naming the command
cmdman status get my-command
cmdman status get my-command --format json
```

## See Also

[cmdman(1)](./cmdman.1.md),
[cmdman-compose-status(1)](./cmdman-compose-status.1.md),
[cmdman-ls(1)](./cmdman-ls.1.md)
