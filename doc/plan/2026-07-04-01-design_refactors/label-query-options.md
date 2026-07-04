# Label-query options under sqlc (OQ4 / D4)

The problem: `store/list.go` `ListCommands` (23-102) and `FindByLabels` (104-127)
append one `AND json_extract(c.JSON, ?) = ?` clause per requested label, plus a
conditional `State IN (?,?,?)` clause. sqlc requires one static SQL string per
generated function, so these cannot port as-is. Semantics to preserve: a row
matches iff **every** requested label key/value pair matches (AND), and state
filtering applies unless `allStates`.

## Option A — keep hand-written

Leave both functions as manual `database/sql` code next to sqlc-generated code.

```go
// store/list.go — unchanged. sqlc simply doesn't own these two queries.
```

- Pros: zero work, zero risk, no schema change.
- Cons: the store permanently has two query styles; the most complex queries are
  exactly the ones without compile-time checking; dynamic string building stays.

## Option B — normalize labels into a table (schema v3)

```sql
-- schema v3
CREATE TABLE CommandLabel (
    ID    TEXT NOT NULL REFERENCES CommandConfig(ID) ON DELETE CASCADE,
    Key   TEXT NOT NULL,
    Value TEXT NOT NULL,
    PRIMARY KEY (ID, Key)
);
CREATE INDEX idx_CommandLabel_KeyValue ON CommandLabel (Key, Value);
```

Migration v3 backfills from existing rows via
`INSERT INTO CommandLabel SELECT c.ID, j.key, j.value FROM CommandConfig c, json_each(c.JSON, '$.labels') j`.
`InsertCommandConfig` and any label-mutating path must write both places (or labels
move out of the JSON blob entirely — a bigger model change).

```sql
-- queries/list.sql (sqlc) — relational division via json_each param for the pairs
-- name: FindByLabels :many
SELECT c.ID FROM CommandConfig c
WHERE (
  SELECT count(*) FROM json_each(@labels) w
  JOIN CommandLabel l ON l.ID = c.ID AND l.Key = w.key AND l.Value = w.value
) = @label_count;
```

(The label set still arrives as one JSON-object parameter — a variable-arity
`IN`-list of pairs is not otherwise expressible in static SQL.)

- Pros: real relational schema, indexable label lookups, sqlc owns everything;
  opens the door to label-key listing/completion features.
- Cons: largest scope — v3 migration + backfill, dual-write (or model change) on
  every config write, most new failure modes. Note the interesting part of the
  query (json_each over a parameter) is exactly the trick Option C uses without
  any of this.

## Option C — static SQL via `json_each` over a JSON parameter (no schema change)

Keep the JSON-blob storage. Make the queries static by passing the requested
labels as one JSON-object parameter and letting SQLite iterate it; compare against
the labels already stored in the row's JSON:

```sql
-- name: ListCommands :many
SELECT c.ID, c.Name, c.CreatedAt, c.JSON, s.State, s.ExitCode, s.JSON
FROM CommandConfig c
JOIN CommandState s ON c.ID = s.ID
WHERE (@all_states OR s.State IN (SELECT value FROM json_each(@states)))
  AND (
    SELECT count(*) FROM json_each(@labels) w
    WHERE EXISTS (
      SELECT 1 FROM json_each(c.JSON, '$.labels') h
      WHERE h.key = w.key AND h.value = w.value
    )
  ) = json_array_length(@states, '$') * 0 + @label_count
ORDER BY c.CreatedAt;
```

(Cleaner spelled with `@label_count` bound directly; shown inline here only to
keep the sketch one statement. `@labels` is `{"k":"v",...}`, `@states` is
`["running",...]`; empty labels object ⇒ count 0 = 0 ⇒ matches all, same as
today.)

```go
// caller side
labelsJSON, _ := json.Marshal(labels)          // map[string]string
statesJSON, _ := json.Marshal(activeStates)    // []string
rows, err := q.ListCommands(ctx, sqlcgen.ListCommandsParams{
    AllStates: allStates, States: string(statesJSON),
    Labels: string(labelsJSON), LabelCount: int64(len(labels)),
})
```

- Pros: fully static SQL ⇒ sqlc owns 100% of queries; no schema change, no
  migration, no dual-write; `FindByLabels` becomes the same shape minus the join.
  Also fixes a latent quirk of the current code: `json_extract` path building
  (`labelJSONPath`, list.go:129-138) can't express keys containing `"` cleanly —
  `json_each` comparison has no path-quoting problem.
- Cons: queries are more exotic (correlated json_each subqueries); label lookups
  still unindexed (full scan — same as today, fine at cmdman's row counts); needs
  minor type-override annotations on the generated params.
- **Prototype gate: PASSED** (2026-07-04, sqlc v1.31.1): `sqlc generate` accepts
  the `json_each` FindByLabels sketch against this schema and emits a working
  `FindByLabels(ctx, FindByLabelsParams)` — verified in scratchpad. Caveat: sqlc
  infers `Labels interface{}` / `LabelCount string`; fix with `CAST(@label_count
  AS INTEGER)` in the query or `overrides:` in sqlc.yaml.

## Comparison

| | A: hand-written | B: label table | C: json_each static |
|---|---|---|---|
| sqlc coverage | ~13/20 queries | all | all |
| Schema change | none | v3 + backfill + dual-write | none |
| Effort | none | L | S-M (plus sqlc-parser prototype) |
| Query perf | scan | indexed | scan (same as today) |
| Risk | none | medium | low (parser gate passed) |

Recommendation: C — the prototype gate already passed, so its main risk is
retired. B only if indexed label queries or label-listing features are actually
wanted.
