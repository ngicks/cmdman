# DECISION — design_refactors

Decision log. One entry per material decision: choice, rationale, rejected
alternatives. Stubs below mirror PLAN.md open questions.

## D1 (from OQ1): Deliverable scope of this plan — RESOLVED 2026-07-04

- Choice: direction + ranking only. Each item gets its own `doc/plan/<topic>-NN`
  entry when picked up; small items (C5, C6, C8, C10) may be executed directly.
- Rationale: matches repo convention of per-topic plan dirs; keeps this plan a
  stable backlog document.
- Rejected: driving execution from this plan (would conflate backlog with work log).

## D2 (from OQ2): Ranking — RESOLVED 2026-07-04

- Choice: draft ranking accepted as-is (C5, C1, C3, C2, C6, C10, C8, C7, C4, C9).
- Rationale: correctness first (C5 latent PID-reuse bug), then seeded items by
  duplication-removed vs effort, hygiene fixes next, widest-churn moves last.
- Rejected: pinning seeded items 1-2-3 to the top regardless.

## D3 (from OQ3): sqlc adoption boundaries — RESOLVED 2026-07-04

- Choice: broader persistence rework — while adopting sqlc, also reconsider the
  migration mechanism and schema, not just the query layer.
- Rationale: maintainer preference; doing schema/migration modernization in the
  same motion avoids touching the store twice.
- Rejected: queries-only minimal adoption.
- Follow-up: exact migration mechanism shape — resolved in D9.

## D4 (from OQ4): Label filtering under sqlc — RESOLVED 2026-07-04

- Choice: Option C — static SQL via `json_each` over a JSON-object parameter; no
  schema change; sqlc owns 100% of queries. Drafts compared in
  `label-query-options.md`; the sqlc-parser gate was prototype-verified
  (sqlc v1.31.1 generates working code for the sketch).
- Rationale: full sqlc coverage without a migration or dual-write; also removes
  the `labelJSONPath` key-quoting quirk. Perf unchanged (full scan, fine at
  cmdman's row counts).
- Rejected: (A) keep hand-written — leaves the most complex queries unchecked;
  (B) CommandLabel table — largest scope, only worth it if indexed label lookups
  or label-listing features become wanted.

## D5 (from OQ5): Include C4 monitor-subpackage extraction — RESOLVED 2026-07-04

- Choice: include, ranked late (rank 9); land C5/C6/C10 (same mon_* files) first.
- Rejected: dropping it — the flat package genuinely conflates responsibilities.

## D6 (from OQ6): Scale-position codec placement — RESOLVED 2026-07-04

- Choice: `pkg/cmdman/mux` — scale cycling is a cmdman feature; muxctl's
  vocabulary stays limited to what its doc.go documents.
- Rejected: pkg/muxctl (would import a cmdman concept into the driver layer);
  leaving in pkg/muxctl/tmux (codec is tmux-free).

## D7 (from OQ7): C3 scope — RESOLVED 2026-07-04

- Choice: tier-1 pure-function hoist only. Enumeration interface deferred until a
  second driver is real.
- Rejected: reifying the enumeration driver contract now — single-implementation
  speculative abstraction.

## D8 (from OQ8): C1 API shape for MuxDown/MuxLs — RESOLVED 2026-07-04

- Choice: methods on `compose.Service` — uniform MuxUp/MuxDown/MuxLs/MuxCycleScale
  surface; cmd and tui_backend call one object the same way, and MuxLs's
  best-effort replica-count resolution already touches the service anyway.
- Rejected: package-level functions (honest signatures, but splits the mux verbs
  across two call styles and forces signature churn if MuxDown/MuxLs later need
  the service).

## D10 (reopens D7): Promote muxctl tier-2 driver-contract extraction — RESOLVED 2026-07-04

- Context: after C3 tier 1 landed, the maintainer reviewed the remaining
  concrete coupling — `pkg/cmdman/mux` imports `pkg/muxctl/tmux` in four files
  (enumeration via `tmux.ListOwnedWindows`/`OwnedWindow`, constructors
  `tmux.New`/`OpenExisting`, cycle-scale primitives `FindLeafPane`/
  `RespawnLeaf`, raw window state `ReadScaleRaw`/`WriteScaleRaw`, plus
  `*tmux.Session.WindowID`/`.Detach` which `muxctl.Session` lacks) — and judged
  it a design defect to fix now, not on driver #2's arrival.
- Choice: reopen D7 and promote the tier-2 extraction to the execution backlog
  (new item C11, ranked immediately after C3, before C2): reify the driver
  contract in `pkg/muxctl` so `pkg/cmdman/mux` becomes tmux-free —
  "tmux-free `pkg/cmdman/mux`" is now a design invariant, not a
  wait-for-second-driver economy.
- Still deferred: the ApplyLayout materialize/split core extraction (parent
  C3 tier-2 third bullet) — it is driver-internal, does not leak into
  `pkg/cmdman/mux`, and remains highest-effort/lowest-urgency.
- Rejected: keeping D7's wait-for-second-driver stance (the leaky boundary
  makes `mux down`/`ls`/`cycle-scale` silently tmux-only and contradicts
  muxctl/doc.go's stated driver contract).

## D9 (from OQ9, spawned by D3): Migration mechanism shape — RESOLVED 2026-07-04

- Choice: embedded `.sql` migration files (embed.FS: `0001_init.sql`,
  `0002_created_at.sql`, ...) executed by the existing hand-rolled
  `DBConfig.SchemaVersion` per-version-transaction walker. sqlc's schema input and
  the migration chain share one source of truth; no new dependency.
- Rejected: keeping the `map[int]func(*sql.Tx) error` scheme (two sources of truth
  vs sqlc's schema.sql); adopting goose/golang-migrate (new dependency and a
  version-table scheme swap for little gain at 2 migrations).
