-- name: InsertCommandExitCode :exec
INSERT INTO CommandExitCode (ID, Timestamp, ExitCode) VALUES (?, ?, ?);

-- name: GetExitHistory :many
SELECT Timestamp, ExitCode FROM CommandExitCode WHERE ID = ? ORDER BY Timestamp;
