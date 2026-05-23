# cmdman compose plan

This document captures the proposed shape for a `cmdman compose` command family.
It is intentionally a discussion draft, not an implementation commitment.

## Goal

Add a cmdman-native compose feature for defining multiple commands in YAML,
creating or running them as normal flat cmdman commands, and grouping them with
labels. The concept borrows from Docker Compose, but the file format and command
semantics are specific to cmdman.

The feature should let users:

- define a set of named commands in one YAML file;
- create or start those commands idempotently;
- group compose-created commands by project labels;
- detect per-command config changes with a config hash;
- operate on all commands in a compose project;
- optionally remove commands that are no longer present in the YAML file.

## Non-goals

Do not port these Docker Compose commands or behaviors:

- `scale`
- `pause`
- `unpause`
- `top`

Do not try to accept Docker Compose YAML. The config format is cmdman-native.

No environment-variable-based project name override. Docker Compose's
`COMPOSE_PROJECT_NAME` (and equivalent vars) are not consulted; project name
comes only from `--project-name` or top-level YAML `name:`.

Interactive attach (terminal multiplexing, foregrounding a command's PTY,
passphrase prompts at compose start) is out of scope for MVP. Commands declared
with `tty: true` are still created with a PTY, but compose itself does not
attach the user's terminal. A future `compose attach <command>` subcommand or
`pkg/mux`-based integration is the planned home for that UX.

## Command surface

Initial subcommands:

- `cmdman compose up`
- `cmdman compose down`
- `cmdman compose create`
- `cmdman compose start`
- `cmdman compose stop`
- `cmdman compose restart`
- `cmdman compose logs`
- `cmdman compose signal`
- `cmdman compose wait`

Likely common flags:

- `-f, --file <path>`: compose YAML path. If omitted, compose searches the
  current working directory for the default file names described below.
- `-p, --project-name <name>`: explicit project/group name.
- `--workdir <path>`: override or set compose work directory.

Subcommands that reconcile desired state should support:

- `--remove-orphan`: remove compose-managed commands that match the project
  label but are no longer present in the YAML.

Current proposal: `create` and `up` support `--remove-orphan`.

Targeting flags for subcommands that can operate on a subset of commands:

- optional positional command names should refer to compose command names, not
  raw cmdman IDs or names;
- no positional command names means all commands in the selected project;
- command filters should be validated against the loaded config when a config
  is loaded;
- for project-only operations that do not require a compose file, command
  filters should match the `cmdman.compose.command` label.

Proposed command-specific target model:

| Subcommand | Requires compose file | Default target set |
| --- | --- | --- |
| `create` | yes | commands in YAML |
| `up` | yes | commands in YAML |
| `start` | no, when `--project-name` is set | commands in YAML when loaded; otherwise all project-labeled commands |
| `stop` | no, when `--project-name` is set | all project-labeled commands |
| `restart` | no, when `--project-name` is set | commands in YAML when loaded; otherwise all project-labeled commands |
| `down` | no, when `--project-name` is set | all project-labeled commands |
| `logs` | no, when `--project-name` is set | all project-labeled commands |
| `signal` | no, when `--project-name` is set | all project-labeled commands |
| `wait` | no, when `--project-name` is set | all project-labeled commands |

For commands that do not require the YAML, project discovery still needs both
a project name and an effective WorkDir. The WorkDir is resolved with the same
rules used during normalization: `--workdir`, then YAML `work_dir` when a file
is loaded, then the process current working directory. The project name comes
from `--project-name` or YAML `name:`. Queries run against the combined
`cmdman.compose.workdir` + `cmdman.compose.project` labels, so the same project
name in a different WorkDir is a different project and is never returned. If
neither `--project-name` nor a discoverable compose file is available, fail
instead of guessing across all compose-managed commands.

## Proposed YAML model

Conceptual Go shape:

```go
type ComposeSpec struct {
	Name     string
	WorkDir  string
	Commands map[string]Command // Raw YAML shape.
}

type NormalizedComposeSpec struct {
	Project  string
	WorkDir  string
	Commands []Command
}

type Command struct {
	// Direct interpretation of cmdman create inputs.
	Name            string
	Dir             string
	Args            []string
	Env             []string
	EnvFile         []EnvFileSpec
	Labels          map[string]string
	RestartPolicy   string
	StopSignal      string
	Tty             bool
	ScrollbackBytes int
	LogDriver       string
	LogOpts         map[string]string

	// Compose-only scheduling/dependency metadata.
	After map[string]AfterSpec
}

type EnvFileSpec struct {
	Path     string
	Required bool // default true
}

type AfterSpec struct {
	Name      string // filled from the map key during normalization.
	Condition string // completed, started, completed_successfully.
}
```

The top-level YAML `commands` field should be a dictionary keyed by command
name. During normalization, each map key is copied into `Command.Name`, and the
commands are reduced to a stable `[]Command`.

Example draft:

```yaml
name: example
work_dir: .

commands:
  api:
    dir: .
    args:
      - go
      - run
      - ./cmd/api
    env:
      - PORT=8080
    env_file:
      - path: .env
        required: false
    restart_policy: on-failure
    stop_signal: SIGTERM
    tty: false
    scrollback_bytes: 1048576
    log_driver: k8s-file
    log_opts:
      max-size: 10m
      max-file: "3"
    labels:
      app: api

  worker:
    args:
      - go
      - run
      - ./cmd/worker
    after:
      api:
        condition: started
```

## Normalization

The compose implementation should parse YAML into a raw config model, then
normalize it before validation, hashing, and reconciliation.

Each compose command should be interpreted as the declarative YAML equivalent of
`cmdman create`. Compose may add fields for env-file loading and dependencies,
but command runtime fields should map directly to `cmdman.CreateRequest` /
`store.CommandConfigJSON` instead of inventing a separate runtime model.

Mapping to the current create surface:

| Compose field | `cmdman create` flag / request field |
| --- | --- |
| `name` | `--name`, `CreateRequest.Name` |
| `dir` | `--dir`, `CreateRequest.Dir` |
| `args` | trailing `COMMAND [ARGS...]`, `CreateRequest.Argv` |
| `env` | `--env`, `CreateRequest.Env` |
| `labels` | `--label`, `CreateRequest.Labels` |
| `restart_policy` | `--restart`, `CreateRequest.RestartPolicy` |
| `stop_signal` | `--stop-signal`, `CreateRequest.StopSignal` |
| `tty` | `--tty`, `CreateRequest.Tty` |
| `scrollback_bytes` | `--scrollback-bytes`, `CreateRequest.ScrollbackBytes` |
| `log_driver` | `--log-driver`, `CreateRequest.LogDriver` |
| `log_opts` | `--log-opt`, `CreateRequest.LogOpts` |

Compose-specific fields:

- `env_file`: load environment entries before building `CreateRequest.Env`.
- `after`: dependency metadata used by compose scheduling.

Rejected fields:

- `auto_remove` (`--rm` / `CreateRequest.AutoRemove`): rejected during
  normalization with a clear error. Compose owns lifecycle, and a self-removing
  command would lose its compose ownership labels along with itself.

Path resolution rule:

- every path in the compose file is resolved from the effective `WorkDir`;
- absolute paths are used as-is;
- this includes command `dir`, `env_file.path`, log option paths such as
  `log_opts.path`, and any future path-valued fields.

## Compose File Discovery

Default compose file names:

1. `cmd-compose.yaml`
2. `cmd-compose.yml`

When `--file` is omitted, compose should look for those file names in the
process current working directory only, in the order above. The first existing
regular file is the effective compose file.

If neither default file exists, the command should fail with a concise error
that names both attempted files and suggests passing `--file`.

If both files exist, `cmd-compose.yaml` wins. This keeps the default selection
deterministic and avoids prompting in non-interactive use.

An explicit `--file` path may point anywhere. Relative explicit file paths are
resolved from the process current working directory, then converted to an
absolute path during normalization.

The compose file's directory is not automatically the effective `WorkDir`.
`work_dir`, `--workdir`, and the default work directory rules below decide
runtime path resolution.

Normalization should:

- resolve the effective compose file path;
- discover `cmd-compose.yaml` / `cmd-compose.yml` from the current working
  directory when `--file` is omitted;
- resolve the effective work directory;
- choose the effective project name;
- normalize command names;
- resolve all relative path fields against the effective work directory;
- reject `auto_remove: true`;
- load env files and apply interpolation as described below;
- merge user labels with reserved compose labels;
- expand `after` map keys into `AfterSpec.Name`;
- apply defaults, including `EnvFileSpec.Required = true` and
  `AfterSpec.Condition = completed`;
- rely on the same defaults as `cmdman create` for omitted runtime fields:
  `restart_policy`, `stop_signal`, `env`, `scrollback_bytes`, and `log_driver`
  should resolve through the existing service/config defaults wherever possible;
- build stable labels and per-command hashes.

### Work directory default

When neither `--workdir` nor YAML `work_dir` is set, the effective `WorkDir`
is the process current working directory at invocation time. The directory
containing the compose file is never an implicit fallback.

### env_file loading

Use `github.com/compose-spec/compose-go/v2/dotenv` for parsing:

- `dotenv.ParseWithLookup(r io.Reader, lookup func(string) (string, bool))`
  for each env file.
- The lookup function provides the values visible to interpolation inside the
  env file (see below).
- `dotenv.Read` errors on missing files. For `EnvFileSpec.Required = false`,
  stat the path first and skip if it does not exist.

Per-command env resolution order (later layers can reference earlier layers):

1. OS environment (base layer).
2. env_file entries, applied in list order. Each file's interpolation lookup
   sees OS env plus any keys set by previously-loaded env_files in the same
   command. Last write wins per key.
3. `env:` entries, applied in list order. Interpolation lookup sees OS env plus
   the merged env_file results. `env:` overrides env_file values on key
   conflicts.
4. `args:` interpolation lookup sees the final per-command env (OS + env_file +
   env).

### Interpolation

`args` values, `env` values, and env_file values support Docker
Compose-compatible interpolation:

- `${VAR}` — substitute the value, or empty string if unset.
- `${VAR:-default}` — substitute the value if set and non-empty, otherwise
  the literal default.
- `${VAR:?error}` — substitute the value if set and non-empty, otherwise fail
  normalization with the given error.

Use the interpolation/template subpackage from
`github.com/compose-spec/compose-go/v2` so behavior matches Docker Compose
exactly. Path-valued log options, `work_dir`, `env_file.path`, and other
non-string-list fields are not interpolated; they are taken literally except
for path resolution against the effective `WorkDir`.

## Project identity

A compose-managed command is identified by three pieces:

- **WorkDir hash**: first 12 hex chars of `sha256(canonical(effective WorkDir))`,
  where `canonical(p)` is `filepath.Clean(filepath.Abs(p))`. Symlinks are
  **not** resolved, so two paths that reach the same physical directory
  through different symlinks produce different WorkDir hashes and different
  project identities. Canonicalization is filesystem-free (no `EvalSymlinks`
  call), so the workdir does not need to exist when the hash is computed.
  Deterministic per canonical path; used to keep generated cmdman names
  unique across WorkDirs without forcing the user to think about it.
- **Project name**: required. Selected with the following precedence:
  1. `--project-name` (CLI override)
  2. top-level YAML `name`

  There is no default. If neither source provides a value, normalization fails
  with a message asking for `--project-name` or a YAML `name:` entry.
  `--project-name` always wins, which is how the same template YAML can be
  instantiated multiple times in one WorkDir.
- **Command name**: the YAML map key.

Generated cmdman name: `<workdir-hash>-<escaped-project>-<escaped-command>`.
Escaping replaces every `-` in the project or command name with `--`. The
workdir-hash is hex and never contains `-`, so the generated form is uniquely
decomposable: scan left-to-right, treat any `-` followed by another `-` as a
single literal `-` belonging to the current component, and treat a `-` not
followed by another `-` as the component separator.

Examples:

- project `devsession`, command `claude`, workdir-hash `a3f9b2c1e8d4` →
  `a3f9b2c1e8d4-devsession-claude`.
- project `dev-session`, command `claude-cli`, workdir-hash `a3f9b2c1e8d4` →
  `a3f9b2c1e8d4-dev--session-claude--cli`.
- project `a-b`, command `c` and project `a`, command `b-c` produce distinct
  generated names (`...-a--b-c` vs `...-a-b--c`), so the previously possible
  alias is eliminated.

Stable identity is the label triple:

- `cmdman.compose.workdir=<absolute-workdir>`
- `cmdman.compose.project=<project>`
- `cmdman.compose.command=<command>`

The workdir-hash appears in the stored cmdman command name (which the cmdman
store enforces unique), but compose itself never queries by it. The canonical
query key is the `cmdman.compose.workdir` label; reconciliation and lookups
always use the labels, never parse the generated name.

Top-level YAML `name` is the only YAML-level vocabulary for project naming:

- Docker Compose users recognize `name`;
- `project` can be confused with labels or workspace concepts;
- `--project-name` remains the CLI override without requiring a separate
  YAML vocabulary.

Project names should be validated with the same conservative rules as command
names: non-empty, path-safe, and label-safe.

### Multi-project per WorkDir

A WorkDir may host multiple projects, provided each has a distinct project
name. Compose enforces this at `create`/`up`: if a different compose-file
path already owns
`(cmdman.compose.workdir=<dir>, cmdman.compose.project=<name>)`, the second
invocation fails with an error that names the conflicting compose file and
suggests `--project-name` to disambiguate. This applies whether the duplicate
name came from YAML `name:` or `--project-name`.

## Labels

Compose-created commands should remain normal flat cmdman commands. Compose
groups them by reserved labels.

Proposed reserved labels:

- `cmdman.compose.project=<project>`
- `cmdman.compose.command=<command-name>`
- `cmdman.compose.config-hash=sha256:<64-hex-chars>`
- `cmdman.compose.version=1`
- `cmdman.compose.workdir=<absolute-workdir>`
- `cmdman.compose.file=<absolute-compose-file>`

`cmdman.compose.workdir` is the canonical scoping label for project discovery.
All subcommands that list, filter, or operate on a project query by the
combination of `cmdman.compose.workdir` and `cmdman.compose.project` (plus
`cmdman.compose.command` when filtering specific commands), rather than parsing
the generated cmdman name. The 12-hex workdir-hash embedded in the generated
name is derived from `cmdman.compose.workdir` on demand and is never stored
separately.

Throughout this document, "project-labeled commands" or "the compose project
label" refers to the (workdir, project) pair: commands whose
`cmdman.compose.workdir` **and** `cmdman.compose.project` labels both match the
effective values. Compose never selects on `cmdman.compose.project` alone.

Users should be allowed to add their own labels later if the base cmdman command
model supports it. Reserved compose labels should be owned by compose.

If a user-provided label uses the `cmdman.compose.` prefix, normalization should
reject it. The prefix check is case-sensitive: only the literal lowercase
`cmdman.compose.` prefix is reserved. Compose should not silently overwrite
user labels because that makes hashing and reconciliation harder to explain.

## Hashing and diffing

Hash identity is per individual command, not whole-file only. This follows the
useful part of Docker Compose behavior: changing one command should only affect
that command.

Hash input should be the normalized command config that affects runtime
behavior. Candidate fields:

- command name;
- normalized argv;
- resolved work directory;
- effective env values (after env_file merge);
- restart policy;
- stop signal;
- TTY setting;
- scrollback bytes;
- log driver and log options;
- user labels, if labels are intended to affect desired command identity;
- dependency/after conditions;
- relevant execution options added later.

Fields that should probably not affect the hash:

- compose file path;
- project name;
- labels used only for grouping;
- comments and YAML formatting;
- generated hash labels.

Current recommendation: hash the normalized runtime config after env files have
been merged, not env file paths or raw contents. If two different env-file
layouts produce the same effective runtime environment, no command recreation is
needed. Missing optional env files also naturally hash as "no entries from that
file".

Hashing implementation should:

- build a small canonical struct instead of hashing `store.CommandConfigJSON`
  directly, because `CommandDir` and generated labels should be excluded;
- sort map keys and env entries deterministically before marshaling;
- hash canonical JSON with SHA-256 and store the full digest as an
  algorithm-prefixed string: `sha256:<64-hex-chars>`. The prefix makes the
  length self-describing, so no truncation is applied and future algorithms
  (e.g. `sha512:`) extend the same scheme without breaking parsers;
- include compose scheduling fields only when they affect the command's
  runtime or start behavior. For MVP, include `after` in the hash so changing
  dependencies is visible to reconciliation, even if the stored cmdman config is
  otherwise unchanged.

## Dependency semantics

Each command may define `after` dependencies by command name.

Supported conditions:

- `completed`: dependency has completed, regardless of exit status. Default.
- `started`: dependency has been started.
- `completed_successfully`: dependency has completed with recorded exit code
  `0`. An absent exit code is treated as not satisfied.

Current recommendations:

- `after` should influence `start` and `up`, not raw creation. `create` may
  validate and hash dependencies, but it should not wait for or start anything.
- dependency cycles should be rejected during normalization.
- `started` passes if the dependency reaches `starting`, `running`, `exited`,
  or `failed` during the current operation, and also passes if the dependency is
  already running before the operation begins.
- `completed` and `completed_successfully` should be evaluated against state
  observed during the current operation for dependencies that compose starts in
  that operation.
- if a dependency was already stopped before the operation begins,
  `completed` and `completed_successfully` may pass based on current stored
  state and exit code. This makes repeated `up worker` usable after a one-shot
  setup command has already completed.

Scheduling uses a DAG. Commands whose dependencies are satisfied are started
concurrently using `golang.org/x/sync/errgroup`. Cycles are rejected during
normalization. Do not hand-wire goroutines and channels for graph execution.

## Lifecycle semantics

### Failure handling

Lifecycle operations that act on multiple commands (`up`/`start`, `stop`,
`down`, `restart`, `signal`, `wait`, and `logs --follow=true`) aggregate
per-command outcomes rather than fail-fast. Every command in the selected set
is attempted; per-command failures are emitted as structured log events
(project, command, operation, underlying error); the subcommand returns a
non-zero exit code if any command failed, after the rest have been processed.

For dependency-ordered operations (`up`/`start`, and the start phase of
`restart` when a compose file is loaded), a command whose dependency cannot
satisfy its `after` condition is skipped and reported, but the rest of the DAG
continues. This preserves "do as much as possible" semantics across bulk
operations.

Within an `errgroup`, the per-command worker returns `nil` and accumulates its
error into a separate sync-safe collection keyed by command name; the parent
compose subcommand assembles the final aggregate error after `Wait()`.

### Subcommands

`compose create`:

- parse and normalize config;
- create missing commands;
- compare existing compose commands by per-command config hash;
- recreate changed stopped commands;
- report changed running commands unless a future force flag is added;
- warn about orphan compose-managed commands in the same project;
- remove orphans when `--remove-orphan` is set.

`compose start`:

- when a compose file is loaded, start commands in dependency order;
- when `--project-name` is set and no compose file is loaded, start commands
  selected by project labels;
- optional command-name filters select a subset, plus dependencies required by
  that subset when a compose file is loaded, unless `--no-deps` is added later;
- without a loaded compose file, command-name filters match
  `cmdman.compose.command` labels directly and do not imply dependencies.

`compose up`:

- perform idempotent convergence;
- create missing commands;
- recreate changed stopped commands;
- report changed running commands unless a future force flag is added;
- start desired commands;
- warn about orphans;
- remove orphans when `--remove-orphan` is set;
- detached by default: does not follow logs or block on running commands.
  A future `--attach`/`--follow` flag can opt into Docker-Compose-style
  attached behavior.

`compose stop`:

- stop all commands with the compose project label;
- when a compose file is loaded, stop in reverse dependency order (dependents
  before dependencies); within each DAG layer, commands stop concurrently;
- when no compose file is loaded, no DAG is available, so all selected
  commands stop concurrently.

`compose restart`:

- when a compose file is loaded, stop YAML commands in reverse dependency
  order, then start them in forward dependency order; within each DAG layer,
  commands run concurrently. Orphans (project-labeled commands absent from
  YAML) are skipped with a warning, consistent with `create`/`up` convergence
  semantics;
- when no compose file is loaded, restart all project-labeled commands; both
  the stop and start phases run concurrently since no dependency graph is
  available;
- optional command-name filters select a subset.

`compose down`:

- stop all commands with the compose project label, then remove them, matching
  user expectation from Docker Compose;
- when a compose file is loaded, the stop phase walks the DAG in reverse
  dependency order (dependents before dependencies); within each layer,
  commands stop concurrently. The remove phase runs after stop completes and
  does not need ordering. When no compose file is loaded, the stop phase is
  fully concurrent;
- because selection is by the (workdir, project) label pair, `down`
  implicitly removes orphans of that pair too. This is intentional: `down` is
  the destructive whole-project teardown, unlike the opt-in `--remove-orphan`
  on `create`/`up`. Commands from another WorkDir that share the project name
  are never touched.

`down` reuses `cmdman.Service.Stop` for the stop step, which sends the
configured stop signal and escalates to SIGKILL after the per-command stop
timeout (inherited from cmdman service defaults; no compose-level override in
MVP, and no `stop_timeout` YAML field). MVP `down` inherits this behavior:
graceful first, hard kill on timeout. A future `--no-kill` flag (paired with
either a gentle-stop service variant that returns an error on timeout instead
of escalating, or a compose-level signal+wait wrapper that does not call
`Stop`) can change this for callers that prefer to fail rather than escalate.
Aggregate failure handling applies: commands that fail to stop or remove are
reported per-command and `down` returns a non-zero exit after processing the
remaining set.

### Empty project target

For subcommands that do not require a compose file (`stop`, `restart`, `down`,
`logs`, `signal`, `wait`), if the resolved project name matches no
project-labeled commands, exit 0 and emit a structured log event describing
the situation (project name, attempted operation). This keeps idempotent
scripting natural while still surfacing typos and empty projects through the
existing root persistent log flags.

`compose logs`:

- show logs for all commands with the compose project label by default;
- optionally support command-name filters;
- reuse existing cmdman log behavior per command;
- when `--follow=false`, merge per-command log streams by timestamp using
  `hiter.MergeFunc` for deterministic, time-ordered output;
- when `--follow=true`, run per-command readers concurrently with `errgroup`
  and tag each line with a `[<command-name>] ` prefix as it arrives.

`compose signal`:

- requires `--signal <SIG>` (for example `--signal SIGTERM`, `--signal HUP`,
  or a numeric value). There is no default — invoking without `--signal`
  fails with a usage error;
- by default sends the signal to all commands with the compose project label;
- positional arguments are command-name filters that narrow the target set;
- the `--signal` value is parsed by the existing cmdman signal parser.

`compose wait`:

- wait for all commands with the compose project label by default;
- optionally support command-name filters;
- return status behavior needs to mirror or extend existing `cmdman wait`.

Current service gaps to account for:

- `cmdman.Service.Remove` already accepts label selectors.
- `cmdman.Service.List` already accepts label selectors.
- `cmdman.Service.Stop`, `Signal`, `Wait`, and `Logs` currently resolve only
  explicit targets.
- `cmdman.Service.Stop` escalates to SIGKILL after the per-command stop
  timeout. `compose down` and `compose stop` inherit this for MVP; a future
  `--no-kill` flag requires either a new service variant that returns an
  error on timeout instead of escalating, or a compose-level wrapper that
  signals + waits without calling `Stop`.
- compose can initially resolve project labels through `List(AllStates: true)`
  and pass concrete IDs to target-only services.
- multi-command logs need a compose-specific aggregator that opens one `Logs`
  reader per resolved ID:
  - `--follow=false`: convert each command's log stream to an iterator and
    merge by timestamp using `github.com/ngicks/go-iterator-helper/hiter.MergeFunc`.
    Output is deterministic and time-ordered across commands.
  - `--follow=true`: run readers concurrently (errgroup), prefix each line
    with `[<command-name>] ` and write as it arrives. Cross-command order is
    not stabilized because future log lines are unknown at write time.

## Orphans

An orphan is a command that:

- has `cmdman.compose.workdir=<effective WorkDir>`;
- has `cmdman.compose.project=<project>`;
- has `cmdman.compose.command=<name>`;
- is not present in the currently loaded compose YAML.

Orphan detection is always scoped to the effective WorkDir. Commands from
another WorkDir that happen to share the project name are not orphans of the
current project and are never affected by `--remove-orphan`.

Default behavior:

- `create` and `up` warn about orphans.

With `--remove-orphan`:

- `create` and `up` remove orphan commands.

In v1, `--remove-orphan` removes only commands that can be removed without
force. Running orphans are reported (structured log event) and left in place.
No `--force-remove-orphan` flag is added in the first release; default
convergence must not unexpectedly terminate processes that disappeared from
YAML.

`down` is different: it is explicitly destructive for the whole project and
should stop then remove project commands.

## Implementation placement

Keep Cobra entrypoints thin.

Suggested packages:

- `cmd/cmdman/commands/compose.go`: Cobra wiring only.
- `pkg/cmdman/compose`: config parsing, normalization, hashing, dependency
  graph, and reconciliation planning.
- `pkg/cmdman/cli`: presentation helpers if compose needs formatted output.

The compose package should expose programmatic operations that are testable
without invoking the CLI.

Suggested internal package boundaries:

- `pkg/cmdman/compose/spec.go`: raw YAML structs and normalized model.
- `pkg/cmdman/compose/load.go`: file discovery, YAML decode, workdir/project
  resolution, path resolution, env-file loading.
- `pkg/cmdman/compose/hash.go`: canonical hash input and hash generation.
- `pkg/cmdman/compose/graph.go`: dependency validation and topological order.
- `pkg/cmdman/compose/plan.go`: compare desired commands with existing
  project-labeled commands and produce create/recreate/orphan actions.
- `pkg/cmdman/compose/service.go`: high-level operations that call
  `cmdman.Service`.

The `compose` package may depend on `pkg/cmdman` and `pkg/cmdman/store`.
`pkg/cmdman` should not depend on `compose`.

## Reconciliation details

Compose should reconcile by label only for commands it owns. The stable
lookup identity is the label triple:

- `cmdman.compose.workdir=<absolute-workdir>`
- `cmdman.compose.project=<project>`
- `cmdman.compose.command=<compose-command-name>`

The actual cmdman command name is deterministic:
`<workdir-hash>-<escaped-project>-<escaped-command>`, where `<workdir-hash>` is
the first 12 hex chars of `sha256(canonical(WorkDir))` (canonicalization
defined under Project identity), and escaping replaces every `-` in the
project or command name with `--`. Because the workdir-hash is hex (no `-`)
and component dashes are always doubled, the generated form is uniquely
decomposable. Stable identity is still the label triple, not the parsed name —
compose never relies on splitting the generated name.

Within-WorkDir project collisions are rejected up front: if a second
`create`/`up` produces the same `(cmdman.compose.workdir, cmdman.compose.project)`
pair as an existing project but originates from a different
`cmdman.compose.file` path, reconciliation aborts with a duplicate-project
error that names both compose files and suggests `--project-name`.

Generated-name collisions (against a non-compose command, or any other
unexpected duplicate) surface as duplicate-name errors from the cmdman store.
Compose wraps these errors with project and command context on both sides so
users can identify the conflict without inspecting labels manually.

For changed commands:

- MVP behavior should delete and recreate when the config hash differs.
- If the command is running, fail unless a `--force-recreate` or equivalent
  flag is added.
- `up` can still start commands that are unchanged or newly created.

This avoids needing an update API in the first implementation. A future update
path can preserve IDs and log history when the underlying fields are safe to
mutate.

Action ordering for `up`:

1. load and normalize desired config;
2. list all existing commands in the project;
3. detect conflicts, changed commands, missing commands, unchanged commands,
   and orphans;
4. warn about orphans; if `--remove-orphan` is set, remove stopped orphans
   (running orphans are still skipped with a structured log event);
5. recreate changed stopped commands;
6. create missing commands;
7. start selected desired commands in dependency order.

Action ordering for `create` is the same, except the final start step is
omitted.

## Name validation

Compose command names and project names should be conservative:

- non-empty after trimming spaces;
- maximum length should be documented; 63 characters is a reasonable initial
  limit for each component;
- allowed characters: ASCII letters, digits, underscore, dot, and hyphen;
- must not start with a dot or hyphen;
- must not contain path separators or whitespace.

These rules make names safe in labels, generated cmdman names, output prefixes,
and future filesystem-adjacent features.

## Testing strategy

Unit tests:

- YAML parsing for the `[]string` `args` form;
- rejection of `auto_remove: true`;
- env file defaults and required/missing behavior;
- env_file / env / args interpolation, including `${VAR}`, `${VAR:-default}`,
  and `${VAR:?error}`;
- env layering order (OS env → env_file → env: → args);
- project name precedence (`--project-name` overrides YAML `name:`);
- mandatory project name: normalization rejects YAML without `name:` when
  `--project-name` is also absent;
- WorkDir default fallback to process CWD;
- workdir-hash determinism: same canonical WorkDir always produces the same
  12-hex prefix; different canonical paths produce different prefixes;
- workdir canonicalization: `./a` and `a` (relative to the same CWD) hash to
  the same prefix; symlinked aliases to the same physical directory hash to
  **different** prefixes (no `EvalSymlinks`);
- generated-name escaping: project and command names containing `-` are
  escaped to `--` in the generated cmdman name; previously aliasing pairs
  such as (project `a-b`, command `c`) and (project `a`, command `b-c`)
  produce distinct generated names;
- within-WorkDir collision rejection: a second `create`/`up` claiming the
  same `(workdir, project)` from a different compose-file path fails with a
  duplicate-project error naming both files;
- dependency normalization and cycle rejection;
- stable hash behavior; `sha256:` prefix and full digest stored;
- orphan detection;
- reconciliation plan generation;
- generated-name collision error wrapping.

E2E tests:

- `compose create` creates flat commands with labels;
- `compose up` is idempotent and stays detached;
- changing one command changes only that command's hash;
- orphan warning appears by default; running orphans are skipped, not killed;
- `--remove-orphan` removes stopped orphans;
- `stop`, `restart`, `logs`, `signal`, and `wait` select commands by project
  label;
- `compose down` removes all project-labeled commands including orphans;
- empty project target on `stop`/`restart`/`down`/`logs`/`signal`/`wait` exits
  0 and emits a structured log event;
- `compose logs --follow=false` produces time-merged output across commands;
- `compose logs --follow=true` tags each line with its command name;
- aggregate-failure semantics: when one command in a multi-command operation
  (e.g. `up`, `down`, `signal`) fails, the remaining commands are still
  processed and the subcommand exits non-zero with per-command error events;
- reverse-dep-order stop: with a YAML dependency chain `worker after api`,
  `compose stop`/`down`/`restart` stops `worker` before `api`;
- `compose signal` without `--signal` fails with a usage error; with
  `--signal SIGHUP` delivers SIGHUP to all selected commands.

Implementation can land in slices:

1. parsing/normalization/hash/plan unit tests with no CLI;
2. `compose create` and `compose up` for create/start happy paths;
3. orphan detection and `--remove-orphan`;
4. `stop`, `restart`, and `down`;
5. `logs`, `signal`, and `wait`;
6. dependency scheduling with `errgroup`.

## Resolved decisions

These items were open in earlier drafts and have since been settled. The
behavior is described in the relevant section above; this list exists as a
quick index.

1. **`compose up` default mode** — detached (create + start, no follow). A
   future `--attach`/`--follow` flag can opt in.
2. **`compose logs` multiplexing** — `--follow=false` merges by timestamp via
   `hiter.MergeFunc`; `--follow=true` runs concurrent readers and tags lines
   with the command name.
3. **Generated cmdman name** — `<workdir-hash>-<escaped-project>-<escaped-command>`,
   where every `-` in the project or command name is escaped to `--`. The
   workdir-hash is hex (no `-`), so the generated form is uniquely
   decomposable; stable identity is still the label triple (see item 16).
   Generated-name collisions can therefore only occur against non-compose
   commands or other unexpected duplicates; compose wraps such errors with
   project and command context.
4. **`--remove-orphan` force option** — not in v1. Running orphans are
   reported and skipped.
5. **Dependency scheduling in first `up`** — full DAG with `errgroup`
   concurrency in v1.
6. **WorkDir default** — process CWD when neither `--workdir` nor YAML
   `work_dir` is set.
7. **`args` YAML form** — `[]string` only.
8. **`auto_remove`** — rejected during normalization.
9. **Interpolation** — `${VAR}` / `${VAR:-default}` / `${VAR:?error}` via
   compose-spec/compose-go in `args`, `env`, and env_file values, with layered
   OS → env_file → env lookup.
10. **env_file parser** — `compose-spec/compose-go/v2/dotenv` with
    `ParseWithLookup`.
11. **Hash format** — `sha256:<full-hex-digest>`, algorithm-prefixed.
12. **`compose restart`** — included in MVP.
13. **Concurrent compose operations** — no locking, matching Docker Compose.
14. **Multi-file `-f` stacking** — not supported.
15. **Empty project targets** — exit 0 with a structured log event.
16. **Project identity** — generated cmdman name is
    `<workdir-hash>-<escaped-project>-<escaped-command>`, with every `-` in
    the project or command name escaped to `--`. WorkDir-hash is the first 12
    hex chars of `sha256(canonical(WorkDir))`, where `canonical(p)` is
    `filepath.Clean(filepath.Abs(p))` (no symlink resolution). The generated
    form is uniquely decomposable, but stable identity is still the label
    triple (`cmdman.compose.workdir`, `cmdman.compose.project`,
    `cmdman.compose.command`); queries use the labels, not the generated name.
17. **Project name** — mandatory. `--project-name` overrides YAML `name:`.
    Normalization fails when neither is set. No name is derived from WorkDir
    or compose-file path.
18. **Within-WorkDir project collisions** — a second `create`/`up` claiming
    an existing `(workdir, project)` from a different compose file is
    rejected with an error naming both compose files.
19. **Interactive attach** — post-MVP. Commands with `tty: true` run
    detached in MVP; the user attaches out-of-band. A future
    `compose attach` subcommand or `pkg/mux` integration owns the
    interactive UX (nvim, passphrase prompts, REPL sessions).
20. **`down` stop semantics** — MVP reuses `cmdman.Service.Stop`, which
    escalates to SIGKILL after the configured stop timeout. A future
    `--no-kill` flag and corresponding gentle-stop service variant can
    replace the escalation with an error.
21. **Failure handling in parallel lifecycle ops** — aggregate-and-continue.
    Every command in the selected set is attempted; per-command errors are
    emitted as structured log events; the subcommand returns a non-zero exit
    only after the remaining set has been processed. Applies to `up`/`start`,
    `stop`, `down`, `restart`, `signal`, `wait`, and `logs --follow=true`.
22. **WorkDir canonicalization** — `filepath.Clean(filepath.Abs(workdir))`.
    Symlinks are not resolved, so different symlinks to the same physical
    directory produce different project identities. Canonicalization is
    filesystem-free; the workdir does not need to exist at normalization time.
23. **Stop ordering** — `stop`, `down`, and `restart` walk the DAG in reverse
    dependency order (dependents before dependencies) when a compose file is
    loaded; within each layer, commands stop concurrently. When no compose
    file is loaded, the stop phase is fully concurrent. `restart`'s start
    phase uses forward dependency order, identical to `up`/`start`.
24. **`compose signal` CLI** — required `--signal <SIG>` flag (no default).
    Positional arguments are command-name filters. Value is parsed by the
    existing cmdman signal parser.
