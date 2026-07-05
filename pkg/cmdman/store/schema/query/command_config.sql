-- name: InsertCommandConfig :exec
INSERT INTO CommandConfig (ID, Name, CreatedAt, JSON) VALUES (?, ?, ?, ?);

-- name: GetCommandConfig :one
SELECT ID, Name, JSON FROM CommandConfig WHERE ID = ?;

-- CAST(... AS TEXT) forces a plain string param: without it sqlc infers the
-- nullable Name/ID columns as sql.NullString here.

-- name: ResolveIDByName :one
SELECT ID FROM CommandConfig WHERE Name = CAST(sqlc.arg(name) AS TEXT);

-- name: ResolveIDByID :one
SELECT ID FROM CommandConfig WHERE ID = ?;

-- name: ResolveIDByPrefix :many
SELECT ID FROM CommandConfig WHERE ID LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%';
