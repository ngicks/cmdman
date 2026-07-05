# store-00-sqlc — sqlc adoption + .sql-file migrations + json_each label queries

One-line summary: Execute backlog item C2 of
`doc/plan/2026-07-04-01-design_refactors/`: rework `pkg/cmdman/store/` so
migrations are embedded `.sql` files (single source of truth), all domain SQL is
sqlc-generated, and the two dynamic label-filter queries become static
`json_each` queries (Option C, prototype-gated).

## Goal / success criteria

- All domain queries in `pkg/cmdman/store/` are sqlc-generated; the two dynamic
  builders (`ListCommands`, `FindByLabels`) and the `fmt.Sprintf` delete loop are
  gone.
- Migrations are embedded `.sql` files (`0001_init.sql`, `0002_created_at.sql`)
  executed by the existing per-version-transaction walker; `createSchema`'s
  hand-maintained parallel DDL is deleted — fresh DBs replay the chain.
- sqlc's schema input is the same `.sql` chain (no drift possible).
- The store's exported API surface is unchanged (same signatures and semantics);
  no caller outside `pkg/cmdman/store/` needs edits.
- `go build ./...`, `go test ./...`, and the e2e suite stay green; label-filter
  behavior parity is covered by new store tests (empty label set, multiple
  labels, keys containing `"`).

## Scope / non-goals

- Scope: `pkg/cmdman/store/` internals, its `internal/migrations` subpackage,
  sqlc tooling wiring (go.mod `tool` directive, `sqlc.yaml`, `queries/*.sql`,
  generated package), regen documentation.
- Non-goals: schema changes (no CommandLabel table — rejected per parent D4);
  changing the store's public API; touching callers; CI enforcement of regen;
  routing schema-management SQL (sqlite_master probes, PRAGMA, version
  read/update) through sqlc (see D3 below).

## Context

Grounding scan 2026-07-05 (full map in orchestration notes; key facts):

- ~20 static SQL statements across `command_config_store.go`,
  `command_state_store.go`, `exit_history.go`, `schema.go`, `migrate.go`,
  `delete.go`; two dynamic builders in `list.go:24-127` (per-label
  `json_extract` predicates via `labelJSONPath`, `list.go:135-138`); one
  `fmt.Sprintf` table-name loop in `delete.go:14`.
- Migrations: `DBConfig(ID=1, SchemaVersion)` row; walker
  `runMigrations` (`migrate.go:17-97`) runs
  `migrations.SchemaMigrations map[int]func(*sql.Tx) error`
  (`internal/migrations/migrations.go:9-11`; only v2 exists:
  `ALTER TABLE CommandConfig ADD COLUMN CreatedAt ...`, `v2.go:7`). Fresh DBs
  bypass the chain: `createSchema` (`schema.go:74-125`) writes current-state DDL
  directly and stamps `schemaVersion = 2` (`schema.go:12`).
- Handle flow: single `*sql.DB` (`store.go:70`); `DeleteCommand` and the
  migration loop use `*sql.Tx` → generated code must target sqlc's `DBTX`
  pattern with `WithTx`.
- Conventions to preserve in wrappers: `nullableString`
  (`command_config_store.go:105-110`), `*int` exit codes from `sql.NullInt64`,
  `json.Marshal`/`Unmarshal` of `model.CommandConfig`/`CommandState` blobs +
  `BackfillDefaults()`, RFC3339 TEXT timestamps.
- Tooling: sqlc not installed; Go 1.26 (go.mod `tool` directive supported).
  Prototype gate for the json_each query passed with sqlc v1.31.1 (parent
  `label-query-options.md`); it infers `Labels interface{}` / `LabelCount
  string` — fix with `CAST(... AS INTEGER)` or `overrides:`.

Parent decisions binding this plan: D3 (broader persistence rework), D4
(Option C json_each, sqlc owns 100% of domain queries), D9 (embedded `.sql`
chain + existing walker, no new dependency).

## Approach

1. **Migration chain first, sqlc second.** The chain becomes sqlc's schema
   input, so it must exist before `sqlc generate` can run.
2. **Fresh DB = replay the chain from version 0.** Delete `createSchema`;
   `0001_init.sql` holds the v1 DDL (current DDL minus `CreatedAt`),
   `0002_created_at.sql` holds the v2 ALTER. The walker gains a version-0 start
   for empty DBs; the pre-DBConfig legacy bootstrap (`migrate.go:28-47`) keeps
   treating an old DB with `CommandConfig` but no `DBConfig` as version 1.
   `schemaVersion` is derived from the embedded chain (max file index), removing
   a sync point.
3. **Generated code stays internal.** sqlc output goes to
   `pkg/cmdman/store/internal/sqlcgen/`; exported `Store` methods become thin
   wrappers, so the public surface is untouched.
4. **Schema-management SQL stays hand-written.** `sqlite_master` probes, PRAGMA
   checks, `DBConfig` version read/update, and migration execution are
   bootstrap code operating before/around the schema sqlc knows — they stay as
   raw `database/sql` (see D3).
5. **Label queries per Option C** (parent `label-query-options.md`): one static
   query with `@labels` as a JSON-object parameter iterated by `json_each`,
   correlated against `json_each(c.JSON, '$.labels')`; `@label_count` bound
   with `CAST` for type inference. Empty label object ⇒ count 0 = 0 ⇒ match
   all (parity with today). `FindByLabels` is the same shape minus the join.

Rejected alternatives are in the parent plan (D4: hand-written / CommandLabel
table; D9: goose / golang-migrate / keeping Go migration funcs).

## Implementation steps

Each step leaves `go build ./...` + `go test ./...` green.

1. **Migration rework** (no sqlc yet).
   - Add `pkg/cmdman/store/internal/migrations/0001_init.sql` (v1 DDL: DBConfig,
     CommandConfig **without** CreatedAt, CommandState, CommandExitCode, all
     indexes and CHECK/FK constraints from `schema.go:75-113`) and
     `0002_created_at.sql` (the `v2.go:7` ALTER).
   - Replace `SchemaMigrations` map + `v2.go` with an `embed.FS` and a small
     accessor (ordered list of `{version, SQL}`; max version exported for
     `schemaVersion`).
   - Rework `runMigrations` (`migrate.go`) to execute `.sql` per version in its
     existing per-version transaction; add version-0 handling for fresh DBs
     (walker inserts/updates the `DBConfig` row after `0001` creates the table).
   - Delete `createSchema`; `initOrCheckSchema`'s fresh-DB path calls the
     walker. Keep the read-only validate path (error telling user to run
     `cmdman migrate`) semantics identical for existing DBs.
   - Verify: store tests + `TestSchemaCreation` still pass; e2e `stale_test`
     passes; a fresh DB ends at the same schema (compare `sqlite_master` SQL
     before/after manually or in a test).
2. **sqlc scaffold + static query port.**
   - `go get -tool github.com/sqlc-dev/sqlc/cmd/sqlc@latest` (pins in go.mod;
     regen = `go tool sqlc generate`).
   - `pkg/cmdman/store/sqlc.yaml`: engine `sqlite`, `schema:` pointing at
     `internal/migrations/` (sqlc applies `.sql` files in lexicographic order),
     `queries: queries/`, output `internal/sqlcgen` with `emit_methods_with_db_argument: false`,
     standard `database/sql` output.
   - Port every static domain statement into `queries/*.sql` named queries:
     config insert/get + `ResolveID`'s three lookups, state insert/update/get,
     exit-history insert/select, and three static deletes replacing the
     `delete.go:14` Sprintf loop.
   - Rewire `Store` methods as wrappers over `sqlcgen.Queries` (constructed once
     in `openStore`; `DeleteCommand` uses `WithTx`). Preserve `nullableString`,
     `*int` exit-code, JSON-blob marshal/unmarshal + `BackfillDefaults`,
     RFC3339 stamps, and exact error wording where tests/callers depend on it
     (`sql.ErrNoRows` passthrough behavior in `GetCommandConfig`/`ResolveID`).
   - Commit generated code. Verify: full test suite.
3. **json_each label queries.**
   - Add parity tests first against the current behavior contract: empty label
     set (matches all), multiple labels (AND), label key containing `"`,
     non-matching value, state filter on/off (`allStates`).
   - Add `ListCommands`/`FindByLabels` to `queries/` per the Option C sketch;
     regen; rewrite `list.go` wrappers to marshal `labels`/`states` to JSON
     params. Delete the dynamic builders and `labelJSONPath`.
   - Verify: new parity tests + existing `TestListCommandsWithLabels` +
     callers' tests (`mon_clean`, `cmdman_list`, compose) + e2e.
4. **Docs + cleanup.**
   - Document the regen step next to the buf convention (README dev section or
     `pkg/cmdman/store/doc.go` comment): `go tool sqlc generate` from
     `pkg/cmdman/store/`.
   - Update parent `2026-07-04-01-design_refactors/STATUS.md` backlog item 5 and
     this plan's STATUS.md.

## Testing / verification

- After each step: `go build ./...`, `go test ./...`.
- After steps 2-3: e2e suite (`go test ./e2e/...`).
- Step 1 adds/keeps a fresh-DB-equals-chain-end-state check; step 3 adds label
  parity tests (these are the behavior-risk hotspots per parent Risks).
- `sqlc generate` must be reproducible: regen and confirm no diff before
  finishing.

## Risks

- sqlc param type inference on json_each queries (`interface{}`/`string`) —
  mitigate with `CAST` annotations; fall back to `sqlc.yaml overrides:`.
- Walker rework touches DB bootstrap for every command — fresh-DB and legacy
  (pre-DBConfig, v1) paths both need explicit tests; e2e is the safety net.
- Network needed once to fetch the sqlc module for the tool directive; if the
  sandbox blocks it, the step is blocked (surface to maintainer, do not vendor
  by hand).
- Generated-code churn in review; regen is manual with no CI check (accepted,
  matches `buf generate` precedent).

## Open questions

None — parent D3/D4/D9 resolved the material choices; implementation-level
choices are recorded in DECISION.md (D1-D4).
