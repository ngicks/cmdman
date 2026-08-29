---
name: go-cmdman-review-checklist
description: "Use this to review your change if you have edited Go code or changed the CLI surface / docs in the project."
---

# cmdman Review Checklist

Project-specific review checklist for cmdman. Apply it **in addition to** `go-review-checklist` and `go-check-outdated-patterns` (those cover generic Go idioms and personal preferences). The items below encode conventions specific to this repo, mostly the `cmdman/compose` service layer.

## Logging

- **DON'T** call `slog.Default()` in service / library code.
- **DO** derive the logger from context with `contextkey.ValueSlogLoggerDefault(ctx)` (from `github.com/ngicks/go-common/contextkey`); it falls back to `slog.Default()` when nothing was injected.
- A function that logs takes `ctx context.Context` as its first parameter. Goroutines log via the captured enclosing `ctx`.
- **DO** reuse the existing `contextkey` helpers (`WithSlogLogger`, `ValueSlogLoggerDefault`, `AppendSlogAttrs`, …) for context-scoped values. **DON'T** hand-roll a `context.WithValue` key type for loggers or attrs.
- Prefer `logger.WarnContext(ctx, …)` / `InfoContext(ctx, …)` over `Warn` / `Info`, but they are ok if ctx is not passed to call site.

## Operation types (compose service layer)

For each operation (`Up`, `Down`, `Stop`, `Start`, `Restart`, `Signal`, `Wait`, `Create`, …):

- **Naming**: aggregate result is `<Op>Result`, per-target record is `<Verb>Outcome`, options are `<Op>Option`. **DON'T** add a redundant domain prefix — `WaitResult`, not `ComposeWaitResult`.
- **Declaration order** within an operation file: `<Op>Option` → `<Op>Result` → `<Verb>Outcome` → methods. (Matches `service_start.go`, `service.go`.)

## YAML / compose spec decoding

- Decoding uses `go.yaml.in/yaml/v4`. **DON'T** silently drop unknown keys, and **DON'T** hard-fail the load on them.
- **DO** capture unrecognized keys with an inline catch-all (`Unknown map[string]any` tagged `yaml:",inline"`) on the raw structs (`RawComposeSpec`, `RawCommand`), then emit one warning per stray key during `Normalize`, iterating keys in sorted order for deterministic output.
- Removed / unsupported config keys (e.g. `auto_remove`) are _ignored with a warning_, not turned into special-cased errors.

## Docs / man page coverage

When a change adds or alters the user-visible CLI surface (a command, subcommand, flag, or spec/YAML field):

- **DO** add/update the man page under `doc/man/`: `cmdman-<name>.1.md` for a top-level command, `cmdman-compose-<name>.1.md` for its compose mirror. Follow the existing section shape: `Name` → `Synopsis` → `Description` → `Options` → `Examples` → `See Also`.
- **DO** list the new command in the command indexes: `doc/man/cmdman.1.md` and (for compose subcommands) `doc/man/cmdman-compose.1.md`.
- **DO** keep `See Also` cross-links **reciprocal**: if page A links to page B, B links back to A — in both the plain and compose variants (compare how `send-keys` ↔ `attach` ↔ `logs` link each other).
- Flag changes update the man page's `Options` section; CLI `--help` stays the authoritative concise flag reference (per `cmdman-help(1)`), man pages carry the prose.
- Spec / config-format changes go to the `.5` pages: `cmdman-compose.5.md`, `cmdman-mux.5.md`, `cmdman-frame.5.md`.
- **DO** update the CLI-surface list in `.apm/instructions/project-overview.local.instructions.md`, then run `apm compile` to regenerate `AGENTS.md` / `.claude/rules/*.local.md`. **DON'T** hand-edit the generated outputs.
- If the plan calls for a README mention, verify it actually landed before checking the docs box in STATUS.md.

## Tests

- **DO** group a subject's tests in a dedicated `<subject>_test.go` (e.g. graph/DAG tests in `graph_test.go`) for discoverability. Keep shared helpers (`testdataPath`, `normalizeFromFile`) in the common test file; tests use the external `compose_test` package.
- **DO** add a test that proves new wiring works end-to-end, not just that it compiles — e.g. inject a logger via `contextkey.WithSlogLogger` and assert the record lands in a captured handler.
- Add `e2e/` coverage when existing tests don't cover a new case (per the project base instructions).
