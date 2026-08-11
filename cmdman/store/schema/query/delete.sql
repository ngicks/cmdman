-- name: DeleteCommandExitCode :exec
DELETE FROM CommandExitCode WHERE ID = ?;

-- name: DeleteCommandState :exec
DELETE FROM CommandState WHERE ID = ?;

-- name: DeleteCommandConfig :exec
DELETE FROM CommandConfig WHERE ID = ?;
