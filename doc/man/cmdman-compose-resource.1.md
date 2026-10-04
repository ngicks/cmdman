# cmdman-compose-resource(1)

## Name

`cmdman compose resource` - read and write the resource values of compose commands

## Synopsis

```text
cmdman compose [selection flags] resource get [--scale N] COMMAND KEY
cmdman compose [selection flags] resource set [--scale N] COMMAND KEY VALUE
cmdman compose [selection flags] resource unset [--scale N] COMMAND KEY
```

## Description

A hook item with a `resource` key acquires a resource for each replica of its
command. When the acquire event exits with code 0, the last line of its stdout
that is not blank, with surrounding white space trimmed, becomes the value of
the resource for that replica. The release event finds the value in
`CMDMAN_COMPOSE_RESOURCE_VALUE` and drops it once it exits with code 0.

Each value is kept by a cmdman command of its own, named
`<replica>.res.<key>`, which is never started. `cmdman ls` lists it, and
`cmdman compose ps` lists it as a `holder` of its project; the compose verbs that
act on a project's commands leave it alone. The value stays readable
after its replica is removed, until the release event or `unset` drops it.

- `get` prints the value followed by a newline. It fails when no value is
  stored for the resource.
- `set` stores VALUE, replacing the value already stored. A replacement keeps
  the release event recorded with the old value; a value stored by `set` for
  the first time has no release event. A replacement that fails leaves the old
  value in place.
- `unset` drops the value without running the release event. Dropping a
  resource that has no value is not an error.

## Selection Flags

Uses the compose selection flags documented in
[`cmdman compose`](./cmdman-compose.1.md): `-f, --file`,
`-p, --project-name`, and `-w, --workdir`.

Without any of them, the project is the one that `CMDMAN_COMPOSE_WORK_DIR` and
`CMDMAN_COMPOSE_PROJECT` name when both are set. Compose sets them for every
replica and every hook, so a hook reaches its own project from any directory.
Otherwise the compose file in the current directory selects the project, as
for the other compose verbs.

## Options

- `--scale N`: address replica N (1-based) of COMMAND. Without it, a process
  whose `CMDMAN_COMPOSE_COMMAND` is COMMAND in the same project addresses its
  own replica, from `CMDMAN_COMPOSE_SCALE_INDEX`. Otherwise a command with one
  replica needs no `--scale`, and one with more requires it. When no replica of
  COMMAND is known any more, the replica the resource is stored for is
  addressed, provided there is only one.

## Examples

Read the directory a `scratch` resource of `web` acquired:

```sh
cmdman compose resource get web scratch
```

Store a value for replica 2 by hand:

```sh
cmdman compose resource set --scale 2 web scratch /tmp/web-2
```

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-compose(5)](./cmdman-compose.5.md),
[cmdman-compose-down(1)](./cmdman-compose-down.1.md)
