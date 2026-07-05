# STATUS — store-00-sqlc

Current state: **implemented** 2026-07-05 (working tree, awaiting maintainer
review). All four steps done; review pass (5-agent) surfaced one blocking
atomicity bug in the legacy-DB bootstrap plus a walker-path coverage gap —
both fixed and re-verified. Full `go test ./...` incl. e2e green; sqlc regen
reproducible. One deviation ratified: sqlc's SQLite engine ignores
`ALTER TABLE ... ADD COLUMN`, so sqlc's schema input is a hand-maintained
`schema.sql` squash guarded by `TestSchemaSQLMatchesMigrationChain` (D5 in
DECISION.md), not the migration chain itself.

## Checklist (mirrors PLAN.md steps)

- [x] 1. Migration rework: embedded `.sql` chain (`0001_init.sql`,
      `0002_created_at.sql`), walker handles version 0, `createSchema` deleted
- [x] 2. sqlc scaffold + static query port (tool directive, sqlc.yaml,
      queries/, internal/sqlcgen, wrapper rewiring incl. DeleteCommand) —
      pinned sqlc v1.31.1; note dependency churn from the tool directive
      (grpc bump + ~20 indirect modules)
- [x] 3. json_each label queries (parity tests first; dynamic builders +
      labelJSONPath deleted) + D5 schema.sql drift test
      (`TestSchemaSQLMatchesMigrationChain`). Intentional behavior change,
      ratified per parent D4: label keys containing `"` now match (previously
      matched nothing — the labelJSONPath quoting quirk); pinned by
      `TestLabelKeyWithDoubleQuote`.
- [x] 4. Docs: store package doc comment (`store.go:1-13`) + sibling bullet
      beside the buf line in `.apm/instructions/project-overview.local.instructions.md:178`
      (`apm compile` run; AGENTS.md/.claude/rules regenerated, both gitignored)
      + parent STATUS.md backlog updated
- [x] Verification: 5-agent review pass (verdict after fixes: findings
      resolved — legacy bootstrap made transactional in `migrate.go:107-131`;
      `migrate_test.go` added covering legacy migration, outdated-DB and
      newer-DB validate errors) + independent build/vet/test pass; regen
      produces no diff (verified twice)

2026-07-05 (later): restructured to the maintainer-specified layout (D6):
`migration/` (chain + embed accessor, public package), `schema/schema.sql`
(hand-maintained squash) + `schema/query/*.sql` (sqlc inputs), `gen/query/`
(sqlc output, package `query`), `gen.go` (`//go:generate go tool sqlc
generate`). Full suite re-verified green; regen stable via
`go generate ./pkg/cmdman/store`.

Next action: maintainer review + commit of the working-tree changes; then
parent backlog item 6 (C6 — broadcaster[T] -race unit test).

Notes for the maintainer:
- Intentional behavior fix: label keys containing `"` now match
  (`TestLabelKeyWithDoubleQuote`); previously they matched nothing.
- The go.mod `tool` directive pulls sqlc's large dependency tree (~20 indirect
  modules; grpc 1.78→1.80, x/term, pflag bumps). e2e green, but if the churn
  is unwanted, a separate tools module is the alternative (parent D4 chose the
  tool directive).
- Pre-existing `apm compile` broken-link warnings in
  `apm_modules/ngicks/agents-package/skills/*` — unrelated to this change.
