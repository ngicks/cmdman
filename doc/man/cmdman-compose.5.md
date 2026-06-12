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
  driver: tmux
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
- `labels`: user metadata. Keys starting with `cmdman.compose.` are reserved.
- `restart_policy`: `no`, `always`, `on-failure`, or `on-failure:N`.
- `stop_signal`: signal used by `cmdman stop` when no signal override is given.
- `tty`: whether the command runs behind a PTY.
- `scrollback_bytes`: scrollback buffer size in bytes. Must be non-negative.
- `log_driver`: `k8s-file` or `none`.
- `log_opts`: driver-specific logging options.
- `after`: dependency map keyed by another command name.

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

Environment values are layered per command:

- Host environment is the base interpolation context, but is not stored.
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
`args`, `env_file.path`, `env` values, and `log_opts.path`.

Supported forms are compose-style variable expressions such as:

- `${VAR}`
- `${VAR:-default}`
- `${VAR-default}`
- `${VAR:?message}`
- `${VAR?message}`
- `${VAR:+replacement}`
- `${VAR+replacement}`

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
[cmdman-compose-mux(1)](./cmdman-compose-mux.1.md)
