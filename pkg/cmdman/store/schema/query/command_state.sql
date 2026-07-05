-- name: InsertCommandState :exec
INSERT INTO CommandState (ID, State, ExitCode, JSON) VALUES (?, ?, NULL, ?);

-- name: UpdateCommandState :exec
UPDATE CommandState SET State = ?, ExitCode = ?, JSON = ? WHERE ID = ?;

-- name: GetCommandState :one
SELECT State, ExitCode, JSON FROM CommandState WHERE ID = ?;
