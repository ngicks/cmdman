CREATE TABLE IF NOT EXISTS DBConfig (
    ID            INTEGER PRIMARY KEY NOT NULL,
    SchemaVersion INTEGER NOT NULL,
    CHECK (ID IN (1))
);

CREATE TABLE IF NOT EXISTS CommandConfig (
    ID              TEXT PRIMARY KEY,
    Name            TEXT UNIQUE,
    JSON            TEXT NOT NULL
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
