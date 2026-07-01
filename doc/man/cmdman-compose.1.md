# cmdman-compose(1)

## Name

`cmdman compose` - reconcile and operate groups of managed commands

## Synopsis

```text
cmdman compose [selection flags] SUBCOMMAND [arguments...]
```

## Project Selection

Compose projects are identified by the pair `(effective workdir, project
name)`, not by project name alone. Commands created from a compose file carry
labels recording that pair, their service name, source file, and normalized
configuration hash. They also record normalized `after` metadata so project-only
lifecycle operations can reconstruct dependency order without loading the file.

- `-f, --file PATH`: load a specific file.
- `-f, --file PROJECT`: resolve a named file under cmdman's config `compose/`
  directory.
- no `-f, --file`: discover `cmd-compose.yaml` or `cmd-compose.yml` in the current
  directory when a subcommand needs a file; some read/lifecycle operations can
  instead select an already stored project.
- `-p, --project-name NAME`: override top-level `name:`.
- `-w, --workdir DIR`: override the effective work directory.

A different compose file cannot silently take ownership of the same
`(workdir, project)` pair. Use a different project name to resolve a collision.

## Compose File

The compose file format is documented in
[cmdman-compose(5)](./cmdman-compose.5.md). In short, a compose file declares a
project name, an effective work directory, command definitions, dependencies,
and optionally a `mux:` dashboard section.

```yaml
name: example
work_dir: .
commands:
  api:
    dir: .
    args: ["./api", "--listen", ":8080"]
    env: ["MODE=dev", "PATH"]
    env_file:
      - path: ${CMDMAN_COMPOSE_DIR}/.env
        required: false
    labels: {role: api}
    restart_policy: on-failure:5
    stop_signal: SIGTERM
    tty: false
    scrollback_bytes: 1048576
    log_driver: k8s-file
    log_opts: {max-size: 10MiB, max-file: "3"}
    after:
      db: {condition: started}
```

Arguments are argv entries, not shell source. Paths resolve relative to the
effective work directory. Interpolation uses `${VAR}`, `${VAR:-default}`,
`${CMDMAN_COMPOSE_DIR}`, and related compose-style forms.

The host environment is available while interpolating, but only values
declared by `env_file` or `env` are stored when a command declares environment
entries. A bare `env` key such as `PATH` copies its current value. This matters
for interactive shells and executables found through `PATH`.

## Reconciliation

`create` and `up` normalize each desired command and compare its configuration
hash with stored project commands:

- absent command: create it;
- matching hash: leave it unchanged;
- changed hash: stop if necessary, remove, and recreate it;
- stored command absent from the file: orphan; retained unless explicitly
  removed.

Dependency edges control start and stop ordering. Start includes recursive
dependencies of named commands and observes `after.condition`. Stop/down of a
named command includes recursive dependents and uses reverse dependency order.
Independent work runs concurrently and failures are aggregated where possible.

## Commands

[config](./cmdman-compose-config.1.md), [create](./cmdman-compose-create.1.md),
[up](./cmdman-compose-up.1.md), [start](./cmdman-compose-start.1.md),
[stop](./cmdman-compose-stop.1.md), [restart](./cmdman-compose-restart.1.md),
[down](./cmdman-compose-down.1.md), [ps](./cmdman-compose-ps.1.md),
[ls](./cmdman-compose-ls.1.md), [inspect](./cmdman-compose-inspect.1.md),
[logs](./cmdman-compose-logs.1.md), [events](./cmdman-compose-events.1.md),
[attach](./cmdman-compose-attach.1.md),
[send-keys](./cmdman-compose-send-keys.1.md),
[signal](./cmdman-compose-signal.1.md), [wait](./cmdman-compose-wait.1.md),
[mux](./cmdman-compose-mux.1.md) (subcommands: `up`, `down`, `ls`, `cycle-scale`).

## See Also

[cmdman(1)](./cmdman.1.md), [cmdman-compose(5)](./cmdman-compose.5.md),
[cmdman-compose-up(1)](./cmdman-compose-up.1.md)
