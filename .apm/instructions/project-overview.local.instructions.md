---
description: "Distilled project overview: architecture, package map, conventions, and tooling for cmdman"
applyTo: "*"
---

# cmdman — Project Overview

> Companion to `base.local.instructions.md`. That file has the short pitch and the
> tooling rules; this file is the distilled architecture + code map. Where the two
> disagree, trust this one — the structure tree in `base` predates the `mux`/`muxctl`
> split and the (now implemented) TUI.

## What it is

A **daemonless** shell-command supervisor in Go. Self-description from `root.go`:
*"podman without pods, the tmux without terminals."* It starts blocking commands in the
background, persists their config/state, and lets you control them over CLI and a TUI.

- Module: `github.com/ngicks/cmdman` · Go `1.26.0`
- Single binary: `cmd/cmdman` → `cmdman`
- Version constant: `pkg/cmdman/version.go` (currently `v0.0.10-devel`), bumped by `internal/cmd/release`.
- Note: README says the TUI is "not implemented" — that is **stale**. The bubbletea TUI in
  `pkg/cmdman/tui` is functional end-to-end.

## Runtime architecture — the mental model

There is **no central daemon**. Every supervised command gets its **own monitor process**.
Two process roles per command:

1. **Service / CLI** (short-lived): the `cmdman <verb>` invocation you type. Stateless across
   calls — everything it needs is on disk (SQLite + per-command dirs). Code: `pkg/cmdman`,
   the `Service` type in `cmdman.go`.
2. **Monitor** (long-lived, detached): supervises one child command. Code: `Monitor` in `mon*.go`.

**Spawn path** (`Service.Start` → `mon_spawn.go`):
- Re-execs the *same binary* with the hidden `__monitor --id <id>` subcommand.
- `detachProcess` (`detach_posix.go`) sets `Setsid=true` and redirects stdio → `/dev/null`;
  CLI calls `cmd.Process.Release()` to orphan it, then polls the SQLite store (every 50 ms)
  for the state to flip `starting` → `running`.

**Monitor lifecycle** (`mon_run.go`, `mon.go`):
- `RunMonitor` opens the store, takes an exclusive `flock` on a PID file (dedupe guard),
  writes its PID + socket path into the state JSON, listens on a Unix socket, serves gRPC.
- `runLoop` re-reads config from disk each iteration (live edits apply on restart) and honors
  `RestartPolicy` (`no` / `on-failure[:N]` / `always`).
- `runOnce` wires the child: PTY (`creack/pty`) when `Tty`, else pipes; sets `Setpgid` and a
  `cmd.Cancel` hook that signals the whole **process group**. Output fans out to: ring buffer
  (scrollback) + log-driver file + a broadcaster (live streams).
- Shutdown: SIGTERM → ctx cancel → signal child's process group → `grpcServer.GracefulStop()`
  → `wg.Wait()`.

**Stale cleanup** (`mon_clean.go`): `Service.List` flips DB entries whose monitor PID is dead
(`kill -0`) to `failed`.

`run` = `create` + `start` (+ optional `--attach`). The hidden `__child` subcommand
(`stdiopipe`) is the popup/TUI stdio relay.

## IPC — gRPC over a per-command Unix socket

- Proto: `pkg/api/schema/proto/cmdman/v1/cmdman.proto` → generated into
  `pkg/api/gen/proto/go/...` via **`buf generate`** (config in `pkg/api/buf.gen.yaml`).
- Socket: `<runtime-dir>/cmd/<id>/monitor.sock`; path is stored in `model.CommandState.SocketPath`
  so any CLI process discovers it without a registry. Transport uses `insecure` creds (local socket).
- Service `CommandMonitorService`:
  - `Attach` — bidi stream: scrollback replay + live stdout; receives stdin bytes / resize events.
  - `Subscribe` — server stream of structured `LogLine` records (used by `logs`).
  - `WriteStdin` / `Signal` / `Stop` (suppresses restart) / `Status`.

## Persistence — SQLite

- Driver: `modernc.org/sqlite` (pure-Go, **CGO-free**). WAL mode, busy-timeout, `foreign_keys=ON`.
- Schema (`store/schema.go`, version **2**): `DBConfig`, `CommandConfig`(id, name, createdAt, JSON),
  `CommandState`(state, exitCode, JSON), `CommandExitCode`(append-only history).
- Domain blobs are `model.CommandConfig` / `model.CommandState` marshaled to the `JSON` columns.
- Migrations: explicit per-version funcs in `store/internal/migrations`, each in its own `Tx`;
  user runs `cmdman migrate` when the on-disk schema is outdated.
- `ResolveID` accepts exact name → exact id → unambiguous id-prefix.

## Package map (current/accurate)

```
cmd/cmdman/                Entry point. Thin cobra wiring ONLY (see conventions).
  main.go                  signal.NotifyContext + commands.Execute
  commands/                one file per subcommand; root.go composes them
  internal/{cmdsignals,stdiopipe}
internal/
  cmd/release              release/version-bump helper
  loggerfactory            slog logger construction from env/flags
  versioninfo              build version info
pkg/api/                   gRPC/proto IPC contract (schema/ + buf-generated gen/)
pkg/cmdman/                Core "usecase" package — the Service + Monitor
  cmdman_*.go              one file per Service verb (start/stop/restart/...)
  mon*.go                  Monitor: spawn, run loop, gRPC server, cleanup
  {detach,prep_process}_posix.go   process detach / pgid (build-tagged posix)
  config*.go               CmdmanConfig: paths, defaults, XDG, watcher kind
  broadcaster.go ringbuffer.go attach_session.go env.go
  cli/                     CLI PRESENTATION layer (tables, progress, attach, templates, tui launch)
  compose/                 docker-compose-like: spec, DAG (graph.go), plan, reconcile engine
  eventlog/                append-only JSONL event log; inotify(linux)/poll watcher
  logdriver/               structured log Writer/Reader; k8sfile/ = podman k8s-file format
  model/                   domain types: CommandConfig, CommandState, EventType, RestartPolicy
  store/                   SQLite config/state/exit-history store + migrations
  tui/                     bubbletea Model/Update/View dashboard (functional)
  internal/flock           advisory file locks (posix flock; no-op error elsewhere)
pkg/muxctl/                driver-agnostic terminal-multiplexer spec + Session/Pane interface
  tmux/                    concrete tmux driver (only driver implemented)
pkg/cmdman/mux/            cmdman's YAML layer: resolves command names → muxctl spec → Run
pkg/hrstr/                 human-readable string/signal parsing
pkg/stdcopy/               demux cmdman's framed log stream into io.Writer (docker-style)
e2e/cmdman/                black-box tests: TestMain builds the binary, drives it as a subprocess
doc/plan/                  old plan files — DO NOT read (per base instructions)
```

**`mux` design principle** (`muxctl/doc.go`): the multiplexer is a **disposable viewer** —
closing/rebuilding a session must never stop a supervised process. Driver autodetect:
`$TMUX`→tmux, `$ZELLIJ`→zellij (errors: not implemented), else tmux.

## CLI surface

Top-level: `attach create events inspect logs ls migrate mux restart rm run send-keys signal
start stop tui version wait compose` (+ hidden `__monitor`, `__child`).
`compose` subcommands mirror the verbs plus `up down config ps`. Most listing/inspect commands
support `--format` Go templates via the shared `cli/template.go` `templateFuncMap`.

## Conventions / codex

**Layering (enforced by `go-design-preference`):**
- `./cmd` parses flags/args and calls a service — **no business logic, no presentation**.
- Presentation (tables, color, progress, tty detection, prompts) lives in `pkg/cmdman/cli`.
- Services are programmatic-caller-first; the CLI is a wrapper. Services never import `./cmd`.
- `main`/`Run` return errors; never `os.Exit` from business code (only `main.go` exits).

**Go idioms used throughout:**
- Context first param; never stored in a struct. Cancellable work takes `ctx`.
- Errors are values: wrap with `fmt.Errorf("...: %w", err)`; `errors.Join` for cleanup; no panic
  for normal failures.
- Concurrency: prefer `golang.org/x/sync/errgroup` / `semaphore` / `singleflight` over hand-wired
  `sync.WaitGroup`+`chan struct{}`. (`Monitor.wg` is a deliberate exception: per-RPC goroutines are
  joined by the supervisor *after* `GracefulStop`, to avoid a stream-handler deadlock.)
- Small interfaces defined at the consumer (`compose.cmdmanSvc`, `cli.AttachSession`,
  `tui.Backend`), not at the implementer.
- Generics for fan-out (`broadcaster[T]`); non-blocking send drops slow consumers.
- DI over package globals; config flows in (flag → env → file → built-in precedence in
  `config.go` `WithDefaults`).

**Logging (project-specific — see `go-cmdman-review-checklist`):**
- In service/library code, **never** `slog.Default()`. Derive from ctx:
  `contextkey.ValueSlogLoggerDefault(ctx)` (from `github.com/ngicks/go-common/contextkey`).
- A function that logs takes `ctx` first; goroutines log via the captured `ctx`.
- Reuse `contextkey` helpers (`WithSlogLogger`, `AppendSlogAttrs`, …); don't hand-roll a context key.
- Prefer `WarnContext`/`InfoContext`. `root.go` injects the logger into the command context.

**Cross-platform build tags:**
- `//go:build !plan9 && !windows && !wasm` → `*_posix.go` (setsid/setpgid/pty, flock).
- `//go:build linux` / `!linux` → inotify vs poll event watcher (`config_{linux,other}.go`).
- `unix` / `windows` / `plan9` variants for file identity (`eventlog/file_ident_*.go`).

**YAML / compose decoding:** uses `go.yaml.in/yaml/v4`. Capture unknown keys with an inline
catch-all (`Unknown map[string]any` `yaml:",inline"`) on raw structs; emit **one warning per stray
key** (sorted order) during `Normalize` — never silently drop, never hard-fail. Removed keys
(e.g. `auto_remove`) are warned, not special-cased into errors.

**Compose operation naming:** aggregate result `<Op>Result`, per-target `<Verb>Outcome`, options
`<Op>Option` — no redundant domain prefix (`WaitResult`, not `ComposeWaitResult`). Declaration
order per op file: `<Op>Option` → `<Op>Result` → `<Verb>Outcome` → methods.

## Build / test / lint / codegen

- Build: `go build -o cmdman ./cmd/cmdman`
- Unit tests live beside code (`_test.go`, often external `_test` package). Run: `go test ./...`
- E2E (`e2e/cmdman`): `TestMain` builds the binary into a temp dir and drives it as a subprocess.
  Add e2e coverage whenever existing tests don't cover a new case.
- Lint/format: **golangci-lint** (`.golangci.yaml`) — staticcheck (all but ST1003), govet
  (gopls-mirrored analyses), modernize, gocritic, `lll` line-length **100**, `goimports`+`golines`.
  PostToolUse hooks auto-run `golangci-lint fmt` + `golangci-lint run` after every Edit/Write
  (`.claude/settings.json`).
- Proto regen: `buf generate` from `pkg/api` (needs `protoc-gen-go`, `protoc-gen-go-grpc`).
- APM primitives: `apm.yml` / `apm.lock.yaml`; `AGENTS.md` is generated (`apm compile`) — don't hand-edit.

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
