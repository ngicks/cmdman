---
description: "Distilled project overview: architecture, package map, conventions, and tooling for cmdman"
applyTo: "*"
---

# cmdman — Project Overview

> Companion to `base.local.instructions.md`. That file has the short pitch and the
> tooling rules; this file is the distilled architecture + code map. Where the two
> disagree, trust this one — `base` keeps only a coarse structure tree.

## What it is

A **daemonless** shell-command supervisor in Go. Self-description from `root.go`:
_"podman without pods, the tmux without terminals."_ It starts blocking commands in the
background, persists their config/state, and lets you control them over CLI and a TUI.

- Module: `github.com/ngicks/cmdman` · Go `1.26.0`
- Single binary: `cmd/cmdman` → `bin/cmdman` (a bare `cmdman` output would collide with the
  top-level source dir of the same name; `/bin` is gitignored).
- Version constant: `internal/libver/libver.go` (currently `v0.0.17-devel`), bumped by the
  **external** release tool
  `go run github.com/ngicks/go-common/tools/bump-libver@latest <release-version>`, which rewrites
  the constant, commits, tags, then bumps to the next `-devel`.
- The bubbletea TUI in `cmdman/tui` is functional end-to-end.

## Runtime architecture — the mental model

There is **no central daemon**. Every supervised command gets its **own monitor process**.
Two process roles per command:

1. **Service / CLI** (short-lived): the `cmdman <verb>` invocation you type. Stateless across
   calls — everything it needs is on disk (SQLite + per-command dirs). Code: `cmdman`,
   the `Service` type in `cmdman/cmdman.go`.
2. **Monitor** (long-lived, detached): supervises one child command. Code: `Monitor` in the
   `cmdman/monitor` package (`mon*.go`).

**Spawn path** (`Service.Start` → `monitor/mon_spawn.go`, `monitor/mon_spawn_posix.go`):

- `SpawnMonitor` re-execs the _same binary_ with the hidden `__monitor --id <id>` subcommand,
  forwarding `--data-dir` / `--runtime-dir` (and `--config` when set) so the child resolves the
  same store and the same file-only settings, then **waits** for that process.
- That first process is only the intermediate of a **double-fork**: `DaemonizeMonitor` does
  `setsid`, re-execs itself as the real daemon with stdio → `/dev/null` and the
  `__CMDMAN_INTERNAL_MONITOR_DAEMON` marker set, releases it (reparented to init), and exits;
  reaping the intermediate avoids a zombie and surfaces daemonization errors.
- The CLI then polls the SQLite store (`WaitForState`, every 50 ms) for the state to flip
  `starting` → `running`.

**Monitor lifecycle** (`monitor/mon_run.go`, `monitor/mon.go`):

- `RunMonitor` opens the store, takes an exclusive `flock` on a PID file (dedupe guard),
  writes its PID + socket path into the state JSON, listens on a Unix socket, serves gRPC.
- `runLoop` re-reads config from disk each iteration (live edits apply on restart) and honors
  `RestartPolicy` (`no` / `on-failure[:N]` / `always`).
- `runOnce` wires the child: PTY (`creack/pty`) when `Tty`, else pipes; sets `Setpgid` and a
  `cmd.Cancel` hook that signals the whole **process group**. Output fans out to: ring buffer
  (scrollback) + log-driver file + a broadcaster (live streams).
- Shutdown: SIGTERM → ctx cancel → signal child's process group → `grpcServer.GracefulStop()`
  → `wg.Wait()`.

**Stale cleanup** (`monitor/mon_clean.go`): `Service.List` flips DB entries whose monitor PID is
dead (`kill -0`) to `failed`.

`run` = `create` + `start` (+ optional `--attach`). The hidden `cmdman tui __child` subcommand is
the TUI's popup child: the parent opens a multiplexer popup running it and the two talk over an
IPC socket. Cancellable stdio for `attach` / `logs` / `events` comes from the external
`github.com/ngicks/go-common/iopipe`.

## IPC — gRPC over a per-command Unix socket

- Proto: `api/schema/proto/cmdman/v1/cmdman.proto` (proto package `cmdman.v1`) → generated into
  `api/gen/proto/go/...` via **`buf generate`** (config in `api/buf.gen.yaml`).
- Socket: `<runtime-dir>/cmd/<id>/monitor.sock`; path is stored in `model.CommandState.SocketPath`
  so any CLI process discovers it without a registry. Transport uses `insecure` creds (local socket).
- Service `CommandMonitorService`:
  - `Attach` — bidi stream: scrollback replay + live stdout; receives stdin bytes / resize events.
  - `Subscribe` — server stream of structured `LogLine` records (used by `logs`).
  - `WriteStdin` / `Signal` / `Stop` (suppresses restart) / `Status`.

## Persistence — SQLite

- Driver: `modernc.org/sqlite` (pure-Go, **CGO-free**). WAL mode, busy-timeout, `foreign_keys=ON`.
- Schema (`store/schema.go`): `DBConfig`, `CommandConfig`(id, name, createdAt, JSON),
  `CommandState`(state, exitCode, JSON), `CommandExitCode`(append-only history).
- Domain blobs are `model.CommandConfig` / `model.CommandState` marshaled to the `JSON` columns.
- Migrations: embedded `NNNN_description.sql` files in `store/migration`, replayed as the whole
  chain for a fresh DB; the schema version is derived from the highest file
  (`migration.MaxVersion()`), so a schema change means adding a file. The user runs
  `cmdman migrate` when the on-disk schema is outdated.
- `ResolveID` accepts exact name → exact id → unambiguous id-prefix.

## Package map (current/accurate)

```
cmd/cmdman/                Entry point. Thin cobra wiring ONLY (see conventions).
  main.go                  cmdsignals.NotifyContext + commands.Execute
  commands/                one file per subcommand; root.go composes them
internal/
  cmdsignals               ExitSignals + thin wrapper over go-common/atomicsignal
  libver                   Version constant, rewritten by the external release tool
  loggerfactory            slog logger construction from env/flags
  templateutil             shared text/template helpers: FuncMap/FuncDocs/FuncHelp
  versioninfo              build version info
api/                       gRPC/proto IPC contract (schema/ + buf-generated gen/)
cmdman/                    Core "usecase" package — the Service
  cmdman_*.go              one file per Service verb (start/stop/restart/...)
  config.go                CmdmanConfig = config.Config type alias
  attach_session.go
  cli/                     CLI PRESENTATION layer (tables, progress, attach, templates, tui launch)
  compose/                 docker-compose-like: spec, DAG (graph.go), plan, reconcile engine
  config/                  canonical config: Config, PartialConfig, Apply, Load, paths, XDG, env
  eventlog/                append-only JSONL event log; inotify(linux)/poll watcher
  frame/                   frame defs: dock components (switcher, command) around
                           the screen edges; ordered carve of the remaining rectangle
  logdriver/               structured log Writer/Reader; k8sfile/ = podman k8s-file format
  model/                   domain types: CommandConfig, CommandState, EventType, RestartPolicy
  monitor/                 Monitor: mon*.go = spawn/double-fork, run loop, gRPC server, cleanup;
                           broadcaster.go ringbuffer.go hooks.go; *_posix.go = detach / pgid
  mux/                     cmdman's YAML layer: resolves command names → muxctl spec → Run
  store/                   SQLite config/state/exit-history store + migration/ chain
  tui/                     bubbletea Model/Update/View dashboard (functional)
  internal/flock           advisory file locks (posix flock; no-op error elsewhere)
pkg/muxctl/                driver-agnostic terminal-multiplexer spec + Session/Pane interface
  tmux/                    concrete tmux driver (only driver implemented)
pkg/hrstr/                 human-readable string/signal parsing
pkg/stdcopy/               demux cmdman's framed log stream into io.Writer (docker-style)
e2e/cmdman/                black-box tests: TestMain builds the binary, drives it as a subprocess
doc/man/                   man pages, written as markdown
doc/plan/                  old plan files — DO NOT read (per base instructions)
```

**`mux` design principle** (`muxctl/doc.go`): the multiplexer is a **disposable viewer** —
closing/rebuilding a session must never stop a supervised process. Driver autodetect:
`$TMUX`→tmux, `$ZELLIJ`→zellij (errors: not implemented), else tmux.

## CLI surface

Top-level: `attach capture-screen config create events inspect logs ls migrate mux restart rm run send-keys
signal start status stop tui version wait compose` (+ hidden `__monitor`, and `tui __child`).
Root carries the persistent flags `--config`, `--data-dir`, `--runtime-dir`.
`config` prints the resolved configuration (indented JSON, or `--format` template).
`status` has `get` / `set` / `delete` subcommands.
`compose` subcommands mirror the verbs plus `up down config ps scale mux`. Most listing/inspect
commands support `--format` Go templates: the generic helpers live in `internal/templateutil`
(`FuncMap`), and `cli/template.go` copies that map and adds its own entries on top.

## Conventions / codex

**Layering (enforced by `go-design-preference`):**

- `./cmd` parses flags/args and calls a service — **no business logic, no presentation**.
- Presentation (tables, color, progress, tty detection, prompts) lives in `cmdman/cli`.
- Services are programmatic-caller-first; the CLI is a wrapper. Services never import `./cmd`.
- `main`/`Run` return errors; never `os.Exit` from business code (only `main.go` exits).

**Go idioms used throughout:**

- Context first param; never stored in a struct. Cancellable work takes `ctx`.
- Errors are values: wrap with `fmt.Errorf("...: %w", err)`; `errors.Join` for cleanup; no panic
  for normal failures.
- Concurrency: prefer `golang.org/x/sync/errgroup` / `semaphore` / `singleflight` over hand-wired
  `sync.WaitGroup`+`chan struct{}`. (`Monitor.wg` is a deliberate exception: per-RPC goroutines are
  joined by the supervisor _after_ `GracefulStop`, to avoid a stream-handler deadlock.)
- Small interfaces defined at the consumer (`compose.cmdmanSvc`, `cli.AttachSession`,
  `tui.Backend`), not at the implementer.
- Generics for fan-out (`broadcaster[T]`); non-blocking send drops slow consumers.
- DI over package globals; config flows in. `cmdman/config` owns the canonical model:
  `Config` (aliased as `cmdman.CmdmanConfig`) is the materialized value type; `PartialConfig` is
  its sparse mirror (nil = absent) and the single decode target for both the config file and the
  environment; `PartialConfig.Apply` is the one merge primitive. `config.Load(flagPath)` layers
  **defaults < config file < environment**, and the `./cmd` wiring applies explicitly-set root
  flags on top via `Flags().Changed` (so an unset flag never clobbers a lower layer).
  - Config file: JSON with **snake_case** keys — `data_dir`, `runtime_dir`, `default_working_dir`,
    `default_scrollback_bytes`, `default_log_driver`, `event_watcher_kind`, `default_hooks`.
    Resolution: `--config` → `$CMDMAN_CONF` → `<user config dir>/cmdman/config.json`.
  - Env layer (caarlos0/env, `CMDMAN_` prefix): only `CMDMAN_DATA_DIR` / `CMDMAN_RUNTIME_DIR`;
    every other field is file-only.
  - `Config.ConfigPath` carries the `--config` value as provenance so a process that re-execs the
    binary (the monitor, the TUI popup child) can forward `--config` — a child inherits the
    environment but not the flags.

**Logging (project-specific — see `go-cmdman-review-checklist`):**

- In service/library code, **never** `slog.Default()`. Derive from ctx:
  `contextkey.ValueSlogLoggerDefault(ctx)` (from `github.com/ngicks/go-common/contextkey`).
- A function that logs takes `ctx` first; goroutines log via the captured `ctx`.
- Reuse `contextkey` helpers (`WithSlogLogger`, `AppendSlogAttrs`, …); don't hand-roll a context key.
- Prefer `WarnContext`/`InfoContext`. `root.go` injects the logger into the command context.

**Cross-platform build tags:**

- `//go:build !plan9 && !windows && !wasm` → `*_posix.go` (setsid/setpgid/pty, flock).
- `//go:build linux` / `!linux` → inotify vs poll event watcher
  (`cmdman/config/config_{linux,other}.go`).
- `unix` / `windows` / `plan9` variants for file identity (`eventlog/file_ident_*.go`).

**YAML / compose decoding:** uses `go.yaml.in/yaml/v4`. Capture unknown keys with an inline
catch-all (`Unknown map[string]any` `yaml:",inline"`) on raw structs; emit **one warning per stray
key** (sorted order) during `Normalize` — never silently drop, never hard-fail. Removed keys
(e.g. `auto_remove`) are warned, not special-cased into errors.

**Compose operation naming:** aggregate result `<Op>Result`, per-target `<Verb>Outcome`, options
`<Op>Option` — no redundant domain prefix (`WaitResult`, not `ComposeWaitResult`). Declaration
order per op file: `<Op>Option` → `<Op>Result` → `<Verb>Outcome` → methods.

## Build / test / lint / codegen

- Build: `go build -o bin/cmdman ./cmd/cmdman` (never `-o cmdman`: that collides with the
  top-level `cmdman/` source dir).
- Unit tests live beside code (`_test.go`, often external `_test` package). Run: `go test ./...`
- E2E (`e2e/cmdman`): `TestMain` builds the binary into a temp dir and drives it as a subprocess.
  Add e2e coverage whenever existing tests don't cover a new case.
- Lint/format: **golangci-lint** (`.golangci.yaml`) — staticcheck (all but ST1003), govet
  (gopls-mirrored analyses), modernize, gocritic, `lll` line-length **100**, `goimports`+`golines`.
  PostToolUse hooks auto-run `golangci-lint fmt` + `golangci-lint run` after every Edit/Write
  (`.claude/settings.json`).
- Proto regen: `buf generate` from `api` (needs `protoc-gen-go`, `protoc-gen-go-grpc`).
- sqlc regen: `go generate ./cmdman/store` (or `go tool sqlc generate` from that dir) — `schema/schema.sql` is sqlc's parser input, kept in sync with `migration/` by `TestSchemaSQLMatchesMigrationChain`; don't hand-edit `gen/query/`.
- Release: `go run github.com/ngicks/go-common/tools/bump-libver@latest <release-version>` — an
  external tool; there is no in-repo release command.
- APM primitives: `apm.yml` / `apm.lock.yaml`; `AGENTS.md` and `.claude/rules/*.local.md` are
  generated (`apm compile`) from `.apm/instructions/*.instructions.md` — edit the sources, then
  recompile; don't hand-edit the outputs.

## Skills to invoke when editing

- **`go-edit-cobra`** — any create/edit under `./cmd/**` (Cobra structure, naming, helpers).
- **`go-cmdman-review-checklist`** + `go-review-checklist` + `go-check-outdated-patterns` — after
  editing Go in this repo.
- Use **context7** for third-party library specifics (bubbletea, cobra, compose-go, grpc, modernc/sqlite).

## Gotchas

- Backward compatibility is **not** a concern — the app was never deployed (`BackfillDefaults` exists
  only for older local DBs).
- The monitor re-execs `os.Executable()`; tests/dev must run the built binary, not `go run` snapshots,
  for `__monitor` to behave.
- Config drift in compose is detected via a `LabelConfigHash` label, not by re-reading the file —
  changing a command's config triggers `ActionRecreate`.
