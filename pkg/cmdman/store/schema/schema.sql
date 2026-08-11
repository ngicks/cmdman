-- schema.sql is sqlc's schema input ONLY. It is a hand-maintained squash of the
-- end state of the embedded migration chain in migration/*.sql (0001 — 0003). It
-- exists because sqlc v1.31.1's SQLite engine silently ignores
-- `ALTER TABLE ... ADD COLUMN` (0002_created_at.sql), so pointing sqlc at the raw
-- chain leaves CreatedAt invisible ("column does not exist"). The runtime still
-- replays migration/*.sql; this file must be kept in sync with that chain's
-- final schema whenever a migration is added.
--
-- CreatedAt is listed last on CommandConfig to mirror the ALTER-appended column
-- order of a live migrated database.

CREATE TABLE IF NOT EXISTS DBConfig (
    ID            INTEGER PRIMARY KEY NOT NULL,
    SchemaVersion INTEGER NOT NULL,
    CHECK (ID IN (1))
);

CREATE TABLE IF NOT EXISTS CommandConfig (
    ID              TEXT PRIMARY KEY,
    Name            TEXT UNIQUE,
    JSON            TEXT NOT NULL,
    CreatedAt       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_command_config_name ON CommandConfig(Name);

CREATE TABLE IF NOT EXISTS CommandState (
    ID              TEXT PRIMARY KEY,
    State           TEXT NOT NULL,
    ExitCode        INTEGER CHECK (ExitCode BETWEEN -1 AND 255),
    JSON            TEXT NOT NULL,
    FOREIGN KEY (ID) REFERENCES CommandConfig(ID)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS idx_command_state_state ON CommandState(State);

CREATE TABLE IF NOT EXISTS CommandExitCode (
    ID              TEXT NOT NULL,
    Timestamp       TEXT NOT NULL,
    ExitCode        INTEGER NOT NULL CHECK (ExitCode BETWEEN -1 AND 255),
    FOREIGN KEY (ID) REFERENCES CommandConfig(ID)
        ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS idx_command_exit_code_id_ts ON CommandExitCode(ID, Timestamp);

-- ComposeHistory: see migration/0003_compose_history.sql for what the columns mean.
CREATE TABLE IF NOT EXISTS ComposeHistory (
    WorkDir  TEXT NOT NULL,
    Project  TEXT NOT NULL,
    File     TEXT NOT NULL,
    LastUsed TEXT NOT NULL,
    PRIMARY KEY (WorkDir, Project)
);
