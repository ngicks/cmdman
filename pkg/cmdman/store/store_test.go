package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

func testEnv() []string {
	return append([]string(nil), os.Environ()...)
}

func TestSchemaCreation(t *testing.T) {
	st := testStore(t)

	var count int
	err := st.DB().QueryRow(`SELECT count(*) FROM CommandConfig`).Scan(&count)
	assert.NilError(t, err)
	assert.Equal(t, count, 0)

	err = st.DB().QueryRow(`SELECT count(*) FROM CommandState`).Scan(&count)
	assert.NilError(t, err)

	err = st.DB().QueryRow(`SELECT count(*) FROM CommandExitCode`).Scan(&count)
	assert.NilError(t, err)
}

func TestOpenStoreConfiguresSQLitePragmas(t *testing.T) {
	st := testStore(t)

	var journalMode string
	err := st.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode)
	assert.NilError(t, err)
	assert.Equal(t, journalMode, "wal")

	var busyTimeout int
	err = st.DB().QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout)
	assert.NilError(t, err)
	assert.Equal(t, busyTimeout, int(sqliteBusyTimeout.Milliseconds()))

	var foreignKeys int
	err = st.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys)
	assert.NilError(t, err)
	assert.Equal(t, foreignKeys, 1)
}

func TestOpenStoreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := OpenStore(ctx, filepath.Join(t.TempDir(), "test.db"), true)
	assert.Assert(t, errors.Is(err, context.Canceled), "got %v", err)
}

func TestExitCodeRangeCheck(t *testing.T) {
	st := testStore(t)

	cfg := &CommandConfigJSON{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		CommandDir:      "/tmp/test",
	}
	assert.NilError(t, st.InsertCommandConfig("test-1", "", cfg))
	assert.NilError(t, st.InsertCommandState("test-1", StateCreated, &CommandStateJSON{}))

	ec := 0
	assert.NilError(t, st.UpdateCommandState("test-1", StateExited, &ec, &CommandStateJSON{}))
	ec = 255
	assert.NilError(t, st.UpdateCommandState("test-1", StateExited, &ec, &CommandStateJSON{}))
	ec = -1
	assert.NilError(t, st.UpdateCommandState("test-1", StateExited, &ec, &CommandStateJSON{}))

	ec = 256
	err := st.UpdateCommandState("test-1", StateExited, &ec, &CommandStateJSON{})
	assert.Assert(t, err != nil, "exit code 256 should be rejected")

	ec = -2
	err = st.UpdateCommandState("test-1", StateExited, &ec, &CommandStateJSON{})
	assert.Assert(t, err != nil, "exit code -2 should be rejected")
}

func TestDeferredForeignKey(t *testing.T) {
	st := testStore(t)

	tx, err := st.DB().Begin()
	assert.NilError(t, err)

	_, err = tx.Exec(`INSERT INTO CommandState (ID, State, JSON) VALUES (?, ?, ?)`,
		"deferred-1", StateCreated, "{}")
	assert.NilError(t, err)

	_, err = tx.Exec(
		`INSERT INTO CommandConfig (ID, Name, JSON) VALUES (?, ?, ?)`,
		"deferred-1",
		nil,
		`{"argv":["/bin/true"],"dir":"/tmp","env":["PATH=/usr/bin:/bin"],`+
			`"command_dir":"/tmp/test","restart_policy":"no","scrollback_bytes":1048576}`,
	)
	assert.NilError(t, err)

	assert.NilError(t, tx.Commit())
}

func TestInsertAndGetCommandConfig(t *testing.T) {
	st := testStore(t)

	cfg := &CommandConfigJSON{
		Argv:            []string{"/bin/bash", "-c", "echo hello"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		Labels:          map[string]string{"app": "test", "env": "dev"},
		CommandDir:      "/tmp/cmd/test-1",
	}

	assert.NilError(t, st.InsertCommandConfig("test-1", "mycommand", cfg))

	id, name, got, err := st.GetCommandConfig("test-1")
	assert.NilError(t, err)
	assert.Equal(t, id, "test-1")
	assert.Equal(t, name, "mycommand")
	assert.Equal(t, got.Argv[0], "/bin/bash")

	id2, _, _, err := st.GetCommandConfig("mycommand")
	assert.NilError(t, err)
	assert.Equal(t, id2, "test-1")
}

func TestListCommandsWithLabels(t *testing.T) {
	st := testStore(t)

	cfg1 := &CommandConfigJSON{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		Labels:          map[string]string{"app": "web", "env": "prod"},
		CommandDir:      "/tmp/cmd/1",
	}
	cfg2 := &CommandConfigJSON{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		Labels:          map[string]string{"app": "api", "env": "prod"},
		CommandDir:      "/tmp/cmd/2",
	}

	assert.NilError(t, st.InsertCommandConfig("id-1", "web", cfg1))
	assert.NilError(t, st.InsertCommandState("id-1", StateRunning, &CommandStateJSON{}))
	assert.NilError(t, st.InsertCommandConfig("id-2", "api", cfg2))
	assert.NilError(t, st.InsertCommandState("id-2", StateRunning, &CommandStateJSON{}))

	entries, err := st.ListCommands(true, map[string]string{"app": "web"})
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
	assert.Equal(t, entries[0].ID, "id-1")

	entries, err = st.ListCommands(true, map[string]string{"env": "prod"})
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 2)
}

func TestDeleteCommand(t *testing.T) {
	st := testStore(t)

	cfg := &CommandConfigJSON{
		Argv:            []string{"/bin/true"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		CommandDir:      "/tmp/cmd/del-1",
	}
	assert.NilError(t, st.InsertCommandConfig("del-1", "", cfg))
	assert.NilError(t, st.InsertCommandState("del-1", StateExited, &CommandStateJSON{}))
	assert.NilError(t, st.InsertCommandExitCode("del-1", 0))

	assert.NilError(t, st.DeleteCommand("del-1"))

	_, err := st.ResolveID("del-1")
	assert.Assert(t, err != nil, "should not find deleted command")
}

func TestConfigJSONMaterialization(t *testing.T) {
	dir := t.TempDir()
	commandDir := filepath.Join(dir, "cmd-1")

	cfg := &CommandConfigJSON{
		Argv:            []string{"/bin/echo", "hello"},
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, cfg.Write())

	data, err := os.ReadFile(filepath.Join(commandDir, "config.json"))
	assert.NilError(t, err)
	assert.Assert(t, len(data) > 0)

	got, err := ReadCommandConfig(commandDir)
	assert.NilError(t, err)
	assert.Equal(t, got.Argv[0], "/bin/echo")
	assert.Equal(t, got.Dir, "/tmp")
	assert.Equal(t, got.Tty, false)
}
