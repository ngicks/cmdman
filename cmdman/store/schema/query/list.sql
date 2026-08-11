-- Option C label filtering: the requested labels arrive as one JSON-object param
-- (@labels = {"k":"v",...}) iterated by json_each and correlated against the
-- labels stored in the row's JSON blob. A row matches when the number of
-- requested pairs that exist on the row equals @label_count, so an empty label
-- object (count 0) matches every row. State filtering passes the active states as
-- a JSON array (@states); @all_states short-circuits it. CASTs pin the param
-- types sqlc would otherwise infer as interface{}.

-- name: ListCommands :many
SELECT c.ID, c.Name, c.CreatedAt, c.JSON AS config_json,
       s.State, s.ExitCode, s.JSON AS state_json
FROM CommandConfig c
JOIN CommandState s ON c.ID = s.ID
WHERE (CAST(sqlc.arg(all_states) AS BOOLEAN)
       OR s.State IN (SELECT value FROM json_each(CAST(sqlc.arg(states) AS TEXT))))
  AND (
    SELECT count(*) FROM json_each(CAST(sqlc.arg(labels) AS TEXT)) w
    WHERE EXISTS (
      SELECT 1 FROM json_each(c.JSON, '$.labels') h
      WHERE h.key = w.key AND h.value = w.value
    )
  ) = CAST(sqlc.arg(label_count) AS INTEGER)
ORDER BY c.CreatedAt;

-- name: FindByLabels :many
SELECT c.ID FROM CommandConfig c
WHERE (
  SELECT count(*) FROM json_each(CAST(sqlc.arg(labels) AS TEXT)) w
  WHERE EXISTS (
    SELECT 1 FROM json_each(c.JSON, '$.labels') h
    WHERE h.key = w.key AND h.value = w.value
  )
) = CAST(sqlc.arg(label_count) AS INTEGER);
