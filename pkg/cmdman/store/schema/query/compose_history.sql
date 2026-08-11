-- name: UpsertComposeHistory :exec
INSERT INTO ComposeHistory (WorkDir, Project, File, LastUsed) VALUES (?, ?, ?, ?)
ON CONFLICT (WorkDir, Project) DO UPDATE SET
    File = excluded.File,
    LastUsed = excluded.LastUsed;

-- name: TouchComposeHistory :execrows
-- TouchComposeHistory bumps recency without creating a row: a start path that
-- never loaded a compose file knows the project but not its File.
UPDATE ComposeHistory SET LastUsed = ? WHERE WorkDir = ? AND Project = ?;

-- name: ListComposeHistory :many
SELECT WorkDir, Project, File, LastUsed FROM ComposeHistory ORDER BY LastUsed DESC, WorkDir, Project;

-- name: GetComposeHistory :one
SELECT WorkDir, Project, File, LastUsed FROM ComposeHistory WHERE WorkDir = ? AND Project = ?;

-- name: DeleteComposeHistory :execrows
-- DeleteComposeHistory forgets one recorded project: the launcher's answer to a
-- history row whose compose file is gone.
DELETE FROM ComposeHistory WHERE WorkDir = ? AND Project = ?;
