# cmdman-compose(5)

## Name

`cmdman compose` file - define a project of managed commands

## Format

A compose file is a YAML document. `cmdman compose` discovers
`cmd-compose.yaml` or `cmd-compose.yml` in the current directory unless a file
is selected with `cmdman compose -f`.

```yaml
name: example
work_dir: .
commands:
  api:
    dir: services/api
    args: ["./api", "--listen", "${API_ADDR:-:8080}"]
    env_file:
      - path: ${CMDMAN_COMPOSE_DIR}/.env
      - path: .env.local
        required: false
    env:
      - MODE=dev
      - PATH
    labels:
      role: api
    restart_policy: on-failure:5
    stop_signal: SIGTERM
    tty: false
    scrollback_bytes: 1048576
    log_driver: k8s-file
    log_opts:
      max-size: 10MiB
      max-file: "3"
    after:
      db:
        condition: started
mux:
  driver:
    name: tmux
  layouts:
    - name: main
      root:
        command: api
```

Unknown YAML fields are ignored with a warning.

## Top-Level Fields

- `name`: project name. Required unless `--project-name` is passed.
- `work_dir`: effective project work directory. Defaults to the current
  directory. `--workdir` overrides it.
- `commands`: map from command name to command definition.
- `mux`: optional dashboard layout used by `cmdman compose mux`; see
  [cmdman-mux(5)](./cmdman-mux.5.md) and [Mux Section](#mux-section) below.

Project and command names must be 1 to 63 characters, must not start with `.`
or `-`, must not contain whitespace or path separators, and may contain only
`A-Za-z0-9._-`.

Paths are resolved relative to the effective `work_dir` after interpolation.
Absolute paths are cleaned and used as-is.

## Command Fields

- `dir`: working directory for the command. Defaults to the effective
  `work_dir`.
- `args`: argv array to execute. Required and not interpreted as shell source.
- `env_file`: ordered list of dotenv files.
- `env`: ordered list of `KEY=VALUE` assignments or bare `KEY` entries.
- `import_host_env`: whether the host environment is imported as the command's
  base environment. Defaults to `true`.
- `inject_env`: whether cmdman injects `CMDMAN_DATA_DIR`, `CMDMAN_RUNTIME_DIR`,
  `CMDMAN_CMD_DATA_DIR`, and `CMDMAN_CMD_ID`. Defaults to `true`.
- `labels`: user metadata. Keys starting with `cmdman.compose.` are reserved.
- `restart_policy`: `no`, `always`, `on-failure`, or `on-failure:N`.
- `stop_signal`: signal used by `cmdman stop` when no signal override is given.
- `tty`: whether the command runs behind a PTY.
- `scrollback_bytes`: scrollback buffer size in bytes. Must be non-negative.
- `log_driver`: `k8s-file` or `none`.
- `log_opts`: driver-specific logging options.
- `after`: dependency map keyed by another command name.
- `hooks`: ordered list of lifecycle hook items. See
  [Lifecycle Hooks](#lifecycle-hooks).

Omitted runtime fields are left for cmdman service defaults. For current
defaults, see [cmdman-create(1)](./cmdman-create.1.md) and
[cmdman-run(1)](./cmdman-run.1.md).

## Environment

Interpolation uses the host environment plus compose path variables:

- `CMDMAN_COMPOSE_FILE`: absolute path of the compose file.
- `CMDMAN_COMPOSE_DIR`: absolute directory containing the compose file.

These variables are interpolation-only unless explicitly copied into `env`.
`CMDMAN_COMPOSE_DIR` is the preferred way to reference files stored beside the
compose file, especially when `work_dir` points somewhere else.

Compose also exposes the project identity. These variables are available to
interpolation, except in `work_dir`, and cmdman injects them into the
environment of every command it creates:

- `CMDMAN_COMPOSE_WORK_DIR`: absolute effective `work_dir`.
- `CMDMAN_COMPOSE_WORK_DIR_HASH`: hash of the effective `work_dir`. Every
  command name generated for the project starts with this hash.
- `CMDMAN_COMPOSE_PROJECT`: project name.

The hash and the project name together identify one project in one work
directory. Use them to name resources that must not collide with another copy
of the project, such as a podman network:

```yaml
commands:
  network:
    args:
      - podman
      - network
      - create
      - --ignore
      - net-${CMDMAN_COMPOSE_WORK_DIR_HASH}-${CMDMAN_COMPOSE_PROJECT}
```

Each replica of a command also receives these variables:

- `CMDMAN_COMPOSE_COMMAND`: command name under `commands`.
- `CMDMAN_COMPOSE_SCALE_INDEX`: 1-based replica index.
- `CMDMAN_COMPOSE_SCALE`: the command's replica count.

Injected variables are applied after `env`, so they override an `env` entry of
the same name. They are injected even when `inject_env` is `false`.

Environment values are layered per command:

- Host environment is the base interpolation context. Compose keeps it out of
  the merged `env_file` and `env` values. The service imports it into the
  created command at create time unless `import_host_env` is `false`.
- `env_file` entries are read in list order. Each file sees the host
  environment and earlier `env_file` values.
- `env` entries are applied in list order and override `env_file` values.
- `args` interpolation sees the final host plus command environment.

An `env_file` item may be a mapping:

```yaml
env_file:
  - path: ${CMDMAN_COMPOSE_DIR}/.env
    required: false
```

`required` defaults to `true`. Missing optional files are skipped.

A bare `env` entry copies the value from the host or merged `env_file`
environment when present. If the key is not defined, it is skipped.

```yaml
env:
  - PATH
  - MODE=${MODE:-dev}
  - COMPOSE_DIR=${CMDMAN_COMPOSE_DIR}
```

Example using compose-relative files while running commands from a separate
workspace:

```yaml
name: api
work_dir: ${CMDMAN_COMPOSE_DIR}/../workspace
commands:
  api:
    dir: api
    args:
      - ./api
      - --config
      - ${CMDMAN_COMPOSE_DIR}/config/api.yaml
    env_file:
      - path: ${CMDMAN_COMPOSE_DIR}/env/api.env
```

## Interpolation

String fields that support interpolation include `work_dir`, command `dir`,
`args`, `env_file.path`, `env` values, `log_opts.path`, and the `args` of hook
events.

Supported forms are compose-style variable expressions such as:

- `${VAR}`
- `${VAR:-default}`
- `${VAR-default}`
- `${VAR:?message}`
- `${VAR?message}`
- `${VAR:+replacement}`
- `${VAR+replacement}`

`$$` stands for a literal `$`.

## Dependencies

`after` declares command dependencies:

```yaml
commands:
  api:
    args: ["./api"]
    after:
      db:
        condition: started
```

Allowed conditions are:

- `completed`: dependency must stop before this command starts. This is the
  default.
- `running`: dependency must reach the running state.
- `completed_successfully`: dependency must stop with a zero exit code.

A command cannot depend on itself. Dependency targets must exist, and cycles are
rejected.

## Logging

`log_driver` may be:

- `k8s-file`: store Kubernetes-style log records.
- `none`: do not store command output.

For `k8s-file`, supported `log_opts` include:

- `path`: log file path. Relative paths resolve from `work_dir`.
- `max-size`: maximum log file size before rotation.
- `max-file`: number of rotated files to retain.

`none` does not accept log options.

## Reconciliation

`cmdman compose create` and `cmdman compose up` normalize each command and hash
the resulting configuration. Existing project commands are compared by stored
labels:

- absent command: create it;
- matching hash: leave it unchanged;
- changed hash: stop if needed, remove, and recreate it;
- stored command absent from the file: orphan.

Orphans are retained unless `--remove-orphan` is used or `compose down` removes
the selected project.

The hash covers `hooks` and the order of their items. Editing a hook therefore
recreates the command. [Lifecycle Hooks](#lifecycle-hooks) lists the hooks a
recreate runs.

## Lifecycle Hooks

`hooks` lists items that run commands around the lifecycle steps of each
replica: create, start, stop, and remove. Only the compose verbs listed under
[Verbs and Events](#verbs-and-events) run hooks.

```yaml
commands:
  web:
    args: [./web]
    hooks:
      - name: migrate
        create_post: [./migrate, up]
      - name: notify
        start_post:
          args: [./notify, started]
          on_error: continue
```

### Hook Items

An item has these fields:

- `name`: required. A hook name follows the rules for command names and must
  be unique within the command.
- `resource`: optional resource key. See [Resources](#resources).
- `create_pre`, `create_post`, `start_pre`, `start_post`, `stop_pre`,
  `stop_post`, `remove_pre`, `remove_post`: the events of the item. An item
  sets at least one event.

An event is either an argv list or a mapping:

```yaml
create_pre: [./prepare, --fast]
create_post:
  args: [./prepare, --check]
  on_error: ignore
```

- The argv list form uses `on_error: fail`.
- The mapping form takes `args` and `on_error`. `args` must not be empty.

`args` is argv and is not interpreted as shell source. Use `[sh, -c, CODE]` to
run shell code. Unknown keys in an item or in an event mapping are ignored with
a warning. `cmdman compose config` prints every event in the mapping form with
`on_error` filled in.

An event fails when its command exits with a non-zero code or ends without an
exit code. `on_error` decides what a failure does:

- `fail`: the default. The failure ends the operation for the replica with an
  error. A failing `create_pre`, `start_pre`, `stop_pre`, or `remove_pre`
  skips the step that follows it. A failing `create_post`, `start_post`,
  `stop_post`, or `remove_post` fails after its step has happened.
- `continue`: the operation goes on. Progress output reports the failure as a
  warning.
- `ignore`: the operation goes on. Progress output reports the failure as
  ignored. A failed release under `ignore` drops the resource value as if the
  release had worked.

The items that set an event run one after another in the order of the `hooks`
list. Different replicas can run their hooks at the same time. Interrupting the
compose verb stops the hook that is running.

### Resources

An item with `resource` acquires a resource for each replica and stores a value
for it. The resource key follows the rules for command names and must be unique
within the command. A resource item sets exactly one acquire event, at most one
release event, and no other event. The release event must match the acquire
event:

- An acquire at `create_pre` or `create_post` pairs with a release at
  `remove_pre` or `remove_post`.
- An acquire at `start_pre` or `start_post` pairs with a release at `stop_pre`
  or `stop_post`.

When the acquire event exits with code 0, cmdman takes the last line of its
stdout that is not blank, trims the surrounding white space, and stores the
result as the value of the resource for the replica. cmdman does not read
stderr. An acquire that prints nothing to stdout stores an empty value. Each
acquire replaces the value stored before it.

Both events find the stored value in `CMDMAN_COMPOSE_RESOURCE_VALUE`. The
variable is empty when no value is stored. A release that exits with code 0
drops the value. A release that fails under `on_error: fail` or
`on_error: continue` keeps the value, and its error names the value.

cmdman never releases a resource on its own. A value is dropped by a release
event that exits with code 0, by a failed release under `on_error: ignore`, or
by [`cmdman compose resource unset`](./cmdman-compose-resource.1.md). A value
without a release event stays until `unset` drops it. Stopping, restarting, or
removing a replica with plain `cmdman` verbs leaves the value in place. The
hooks own whatever the value names, such as a directory or a port.
[`cmdman compose down`](./cmdman-compose-down.1.md) retries the stored release
of a value whose replica is gone.

[`cmdman compose resource get`](./cmdman-compose-resource.1.md) prints a value.

### Intermediate Commands

The hooks of a replica create two kinds of cmdman commands: exec commands and
holders. Together they are the intermediates of the replica. In the names
below, `<replica>` is the cmdman command name of the replica. The `NAME` column
of `cmdman compose ps` shows it.

- An exec command runs one event of one item. Its name is
  `<replica>.hook.<item>.<event>`. It runs in the working directory of the
  replica with the environment of the replica, without a PTY, with
  `restart_policy: no` and the `k8s-file` log driver. cmdman removes it when
  the event succeeds. A failed event keeps it under every `on_error`. Read
  the failure with `cmdman logs <replica>.hook.<item>.<event>` or
  `cmdman inspect`. The next run of the same event for the same replica
  replaces it. A compose verb that removes the replica also removes every
  exec command of the replica that is not running. A recreate, the removal of
  a surplus replica, `--remove-orphan`, and `compose down` all remove a
  replica.
- A holder keeps one resource value. Its name is `<replica>.res.<key>`. cmdman
  never starts it. A holder outlives its replica. A `remove_post` release and
  a later `compose down` still find the value.

Intermediates carry the labels `cmdman.compose.hooks.project` and
`cmdman.compose.hooks.workdir` in place of `cmdman.compose.project` and
`cmdman.compose.workdir`. The compose verbs that act on the commands of a
project never select them. They appear in these listings:

- `cmdman ls` lists holders and running exec commands. `cmdman ls --all` also
  lists the exec commands of failed events.
- [`cmdman compose ps`](./cmdman-compose-ps.1.md) lists both kinds under their
  project, with `KIND` and `OWNER` columns.
- [`cmdman compose ls`](./cmdman-compose-ls.1.md) counts them in
  `INTERMEDIATES`.

### Verbs and Events

Each replica runs the events of its own hooks. Each entry below lists the
events in the order they run, with the step between them.

- `compose create` and `compose up` run `create_pre`, create, `create_post` for
  every new replica.
- They recreate a changed replica in two parts. The old replica runs
  `stop_pre`, stop, `stop_post` when it is starting or running, and then
  `remove_pre`, removal, `remove_post`. The new replica runs `create_pre`,
  create, `create_post`.
- They remove a surplus replica of a scale-down the same way. It runs
  `stop_pre`, stop, `stop_post` when it is starting or running, and then
  `remove_pre`, removal, `remove_post`.
- With `--remove-orphan`, every stopped orphan runs `remove_pre`, removal,
  `remove_post`. A running orphan is skipped and runs no hooks.
- `compose start` and `compose up` run `start_pre`, start, `start_post` for
  every replica that is neither starting nor running. `compose up` does not
  start a replica whose create or recreate failed in the same run, and runs no
  start hooks for it.
- `compose stop` runs `stop_pre`, stop, `stop_post` for every starting or
  running replica.
- `compose restart` runs `stop_pre`, stop, `stop_post` for every starting or
  running replica. It then runs `start_pre`, start, `start_post` for every
  replica. A replica whose stop hook failed is not started.
- `compose down` runs `stop_pre`, stop, `stop_post` for every starting or
  running replica. It then runs `remove_pre`, removal, `remove_post` for every
  replica. [cmdman-compose-down(1)](./cmdman-compose-down.1.md) describes the
  releases it retries.
- `compose scale` runs the hooks of `compose up` for the commands it names.

A replica that a verb leaves unchanged runs no hooks.

Each replica stores a copy of the hooks it was created with. The stop and
remove events always come from that copy. `compose stop`, `compose restart`,
and `compose down` therefore run them without a compose file. A recreate runs
the old hooks of the replica it replaces. The create events come from the
compose file. The start events come from the compose file when the verb loads
one, and from the stored copy otherwise.

Editing a hook changes the configuration hash. The next `compose create` or
`compose up` recreates the command. The old replica runs its stored stop and
remove events. The new replica runs the edited create events, and `compose up`
also runs its edited start events.

Plain `cmdman start`, `cmdman stop`, `cmdman restart`, and `cmdman rm` run no
hooks, even on a compose replica. A restart by `restart_policy` runs no hooks
either.

### Hook Environment

A hook command runs with the environment of its replica. That environment
includes `CMDMAN_COMPOSE_WORK_DIR`, `CMDMAN_COMPOSE_WORK_DIR_HASH`,
`CMDMAN_COMPOSE_PROJECT`, `CMDMAN_COMPOSE_COMMAND`,
`CMDMAN_COMPOSE_SCALE_INDEX`, and `CMDMAN_COMPOSE_SCALE`, as described under
[Environment](#environment). cmdman adds these variables:

- `CMDMAN_COMPOSE_HOOK_NAME`: the `name` of the item.
- `CMDMAN_COMPOSE_HOOK_EVENT`: the event that runs, such as `start_pre`.
- `CMDMAN_COMPOSE_RESOURCE_KEY`: the `resource` key. cmdman sets it for a
  resource item only.
- `CMDMAN_COMPOSE_RESOURCE_VALUE`: the stored value of the resource, empty when
  no value is stored. cmdman sets it for a resource item only.
- `CMDMAN_DATA_DIR` and `CMDMAN_RUNTIME_DIR`: the directories of the cmdman
  that runs the hook. A `cmdman` started by the hook reaches the same store.
- `CMDMAN_CONF`: the absolute path of the configuration file. cmdman sets it
  when it runs with `--config`.
- `CMDMAN_CMD_ID` and `CMDMAN_CMD_DATA_DIR`: the ID and data directory of the
  exec command. They do not name the replica.

cmdman interpolates hook `args` when it loads the compose file. Hook `args` see
the same variables as the `args` of the command. Apart from
`CMDMAN_COMPOSE_WORK_DIR`, `CMDMAN_COMPOSE_WORK_DIR_HASH`, and
`CMDMAN_COMPOSE_PROJECT`, the variables above do not exist at that time. `$CMDMAN_COMPOSE_RESOURCE_VALUE` in shell code
therefore becomes an empty string before the hook runs. Write `$$` to pass a
`$` through to the shell:

```yaml
remove_post: [sh, -c, 'rm -rf -- "$$CMDMAN_COMPOSE_RESOURCE_VALUE"']
```

An argv entry sees a variable only through interpolation. A hook that reads a
variable at run time needs a shell or a program that reads its environment.

### Progress Output

`compose create`, `compose up`, `compose start`, `compose stop`,
`compose restart`, `compose down`, and `compose scale` report every hook run in
their progress output. In `tty` mode, each hook run gets a line labeled
`<command> hook <item>.<event>`. The line shows the latest output of the hook
while it runs.

In `json` mode, a record of a hook run sets `hook`, `lifecycle`, `exec`, and
`scaleIndex` to the item name, the event, the exec command name, and the
replica index. Its `phase` is one of these values:

- `hook-running`: the hook command started.
- `hook-succeeded`: the hook exited with code 0.
- `hook-failed`: the hook failed under `on_error: fail`, or the verb was
  interrupted.
- `hook-warning`: the hook failed under `on_error: continue`, or under
  `on_error: fail` with `compose down --force`.
- `hook-ignored`: the hook failed under `on_error: ignore`.
- `hook-output`: one line of hook output. `line` holds the line, and `stream`
  holds `stdout` or `stderr`.

The record that ends a run carries `exitCode` when the hook exited, and
`error` when the hook failed.

`compose down --force` also writes a `hook-warning` record that sets no
`hook`, `lifecycle`, `exec`, or `scaleIndex`. That record belongs to the
replica itself. Down could not decode the hooks stored on the replica and tore
the replica down without them. Its `error` holds the decoding error.

`compose create` and `compose restart` write one result line per outcome to
standard output after their progress output. The other verbs write only
progress output.

### Example

```yaml
name: example
commands:
  web:
    scale: 2
    args: [./web]
    hooks:
      - name: scratch
        resource: scratch
        create_pre: [mktemp, -d]
        remove_post: [sh, -c, 'rm -rf -- "$$CMDMAN_COMPOSE_RESOURCE_VALUE"']
      - name: runlog
        resource: runlog
        start_pre:
          - sh
          - -c
          - 'echo "$$(pwd)/web-$$CMDMAN_COMPOSE_SCALE_INDEX-$$(date +%s).log"'
        stop_post:
          args: [sh, -c, 'gzip -- "$$CMDMAN_COMPOSE_RESOURCE_VALUE"']
          on_error: continue
      - name: notify
        start_post:
          args: [sh, -c, 'echo "web-$$CMDMAN_COMPOSE_SCALE_INDEX up" >> events.log']
          on_error: ignore
```

- Each replica of `web` gets a directory from `mktemp -d` before cmdman creates
  it. `remove_post` deletes the directory after cmdman removes the replica.
- Each start of a replica records a new log file path. `stop_post` compresses
  the file after the replica stops. A failed compression is reported as a
  warning and keeps the value until the next start replaces it.
- `notify` appends a line to `events.log` after each start. Its failure never
  fails the start.
- `./web` reads its own values with `cmdman compose resource get web scratch`
  and `cmdman compose resource get web runlog`.

## Mux Section

The optional `mux:` top-level field embeds a mux spec body (see
[cmdman-mux(5)](./cmdman-mux.5.md)). Leaves name compose services from the
same file's `commands:` map.

At file load time, `cmdman compose` applies the following static validation
rules to `mux:` leaves:

- **Unknown command**: every leaf `command` must name a key in `commands:`.
  A leaf referencing an unknown service is an error:
  ```
  mux: layout "<name>": leaf "<command>": unknown command
  ```
- **Pinned scale exceeds command scale**: a leaf with `scale: N` must satisfy
  `N <= commands.<command>.scale`. For example, pinning `scale: 3` on a command
  declared with `scale: 2` is rejected:
  ```
  mux: layout "<name>": leaf "<command>": scale 3 exceeds commands.<command>.scale 2
  ```
  A command without a `scale:` field in `commands:` normalizes to `scale: 1`.
- **Absent scale (cycle-scale target)**: a leaf with no `scale:` (or `scale: 0`)
  is a cycle-scale target and is never rejected by static validation. Live
  divergence (e.g. `cmdman compose scale web=5`) is handled at resolution time.

These rules are spec-vs-spec only. Live replica count changes after file load
are handled by the existing resolver, which errors on a missing live replica.

## See Also

[cmdman-compose(1)](./cmdman-compose.1.md), [cmdman-mux(5)](./cmdman-mux.5.md),
[cmdman-compose-mux(1)](./cmdman-compose-mux.1.md),
[cmdman-compose-resource(1)](./cmdman-compose-resource.1.md)
