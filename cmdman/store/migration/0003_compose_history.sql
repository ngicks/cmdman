-- ComposeHistory records the last time each compose project was brought up, so
-- the launcher can offer projects that no longer have any stored command.
--
-- The key is the (WorkDir, Project) pair that is compose project identity
-- everywhere else (label filter, service_list grouping, GenerateProjectIdentity);
-- WorkDir carries the same canonicalization as compose/hash.go's input —
-- filepath.Clean(filepath.Abs(p)), no symlink resolution — so history keys and
-- mux identities agree. Project is '' for an unnamed project.
--
-- File is the resolved absolute compose file path (what LabelFile records), kept
-- as "last used" rather than as part of the key: re-upping a project with a
-- different -f overwrites it. LastUsed is RFC3339 in UTC.
CREATE TABLE IF NOT EXISTS ComposeHistory (
    WorkDir  TEXT NOT NULL,
    Project  TEXT NOT NULL,
    File     TEXT NOT NULL,
    LastUsed TEXT NOT NULL,
    PRIMARY KEY (WorkDir, Project)
);
