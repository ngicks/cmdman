# DECISION — store-00-sqlc

Material choices were made in the parent plan
(`doc/plan/2026-07-04-01-design_refactors/DECISION.md` D3, D4, D9). Entries here
are implementation-level decisions taken while translating those into steps.

## D1: Generated code location — internal/sqlcgen — 2026-07-05 (location superseded by D6; wrapper rationale stands)

- Choice: sqlc output goes to `pkg/cmdman/store/internal/sqlcgen/`; exported
  `Store` methods stay as thin hand-written wrappers.
- Rationale: keeps the store's public API byte-identical (14+ caller files
  across pkg/cmdman, cli, compose, e2e stay untouched); generated types
  (NullString params, string timestamps) never leak to callers.
- Rejected: generating into the store package itself (leaks generated names,
  risks collisions with existing exported symbols).

## D2: Fresh DB replays the migration chain; schemaVersion derived from it — 2026-07-05

- Choice: delete `createSchema` (`schema.go:74-125`); a fresh DB starts at
  version 0 and replays `0001_init.sql`, `0002_created_at.sql`, ... The
  `schemaVersion` constant is replaced by the max version in the embedded
  chain.
- Rationale: D9 mandates one source of truth; keeping a parallel current-state
  DDL string reintroduces exactly the drift D9 eliminates, and sqlc reads the
  same chain so drift would break silently at generate time.
- Rejected: keeping `createSchema` synced by hand (two sources of truth); a
  generated squashed schema.sql (extra tooling for 2 migrations).

## D3: Schema-management SQL stays hand-written — 2026-07-05

- Choice: "sqlc owns 100% of queries" (parent D4) is read as 100% of *domain*
  queries. `sqlite_master` existence probes (`schema.go:37,131`,
  `migrate.go:28`), the JSON1 capability probe, PRAGMA checks, `DBConfig`
  version read/update, and migration-DDL execution remain raw `database/sql`.
- Rationale: these run before/around the schema sqlc knows (sqlite_master is
  not in the schema; migration DDL is by definition schema-changing) — sqlc
  cannot type-check them and would reject the table references.
- Rejected: forcing them through sqlc (not expressible / meaningless
  compile-time check).

## D5 (amends D2, deviates from parent D9): sqlc schema input is a squash schema.sql + drift test — 2026-07-05

- Context: during step 2, sqlc v1.31.1's SQLite engine was found to silently
  ignore `ALTER TABLE ... ADD COLUMN` (reproduced in isolation: `RENAME COLUMN`
  registers, `ADD COLUMN` does not), so pointing `schema:` at the migration
  chain leaves `CommandConfig.CreatedAt` (added by `0002_created_at.sql`)
  invisible and generation fails on queries referencing it. The "sqlc reads the
  same chain" design (parent D9 / this plan's D2) is not implementable with
  current sqlc.
- Choice: keep the runtime chain as the single source of truth for *databases*;
  add a hand-maintained `pkg/cmdman/store/schema.sql` used **only** as sqlc's
  parser input, guarded by a drift test that applies `schema.sql` to a scratch
  DB and asserts its `sqlite_master` state equals a chain-migrated fresh DB's.
- Rationale: the drift D9 wanted to eliminate is made loud at `go test` time
  (strictly better than "breaks silently at generate time"); the workaround
  touches no runtime path, no public API, no caller, and is reversible if a
  future sqlc parses ALTER ADD COLUMN.
- Rejected: rewriting `0002` as a table-rebuild migration so the chain parses
  (adds real runtime migration risk — FK-referenced table drop/rename — to
  solve a codegen-input problem); a generated squash via custom dump tooling
  (drift test gives a near-equivalent guarantee without new tooling).

## D4: sqlc pinned via go.mod tool directive, sqlc.yaml beside the store — 2026-07-05

- Choice: `go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc` (Go 1.26 tool
  directive); `sqlc.yaml` + `queries/` live in `pkg/cmdman/store/`; regen is
  `go tool sqlc generate` run from that directory, documented alongside the
  `buf generate` convention.
- Rationale: parent plan named the tool directive; keeps versioning in go.mod
  with no separate tools module; config-beside-consumer mirrors
  `pkg/api/buf.gen.yaml`.
- Rejected: globally installed sqlc (unpinned, breaks reproducible regen);
  separate tools module (extra ceremony for one tool).

## D6: Directory layout — migration/, schema/query/, gen/query/, gen.go — 2026-07-05

- Choice (maintainer-specified): `pkg/cmdman/store/migration/` holds the
  `.sql` chain + embed accessor (package `migration`, not under `internal/`);
  `schema/schema.sql` (hand-maintained squash, unchanged role per D5) +
  `schema/query/*.sql` are sqlc's inputs; `gen/query/` is sqlc's output
  (package `query`); `gen.go` at the store root carries the
  `//go:generate go tool sqlc generate` hook. Singular directory names.
- Rationale: mirrors the `pkg/api` schema/gen split; go:generate makes regen
  discoverable via `go generate ./...`.
- Rejected: generating the migration accessor or the squash via custom
  tooling (maintainer kept the squash hand-maintained; drift test remains the
  guard); keeping generated code under `internal/` (maintainer chose a public
  `gen/`, matching `pkg/api/gen`).
