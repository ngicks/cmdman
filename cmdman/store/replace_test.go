package store

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/ngicks/cmdman/cmdman/model"
	"gotest.tools/v3/assert"
)

func replaceTestConfig(commandDir string, argv ...string) *model.CommandConfig {
	return &model.CommandConfig{
		Argv:            argv,
		Dir:             "/tmp",
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: DefaultScrollbackBytes,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}
}

// insertExitedCommand stores a command that has run once and exited, so a test
// can tell whether its state and exit history survive.
func insertExitedCommand(t *testing.T, st *Store, id, name string, argv ...string) {
	t.Helper()
	assert.NilError(t, st.InsertCommandConfig(id, name, replaceTestConfig("/tmp/cmd/"+id, argv...)))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeExited, &model.CommandState{}))
	assert.NilError(t, st.InsertCommandExitCode(id, 3))
}

func TestReplaceCommand(t *testing.T) {
	st := testStore(t)
	insertExitedCommand(t, st, "old-1", "x", "echo", "a")

	newCfg := replaceTestConfig("/tmp/cmd/new-1", "echo", "b")
	assert.NilError(t, st.ReplaceCommand("old-1", "new-1", "x", newCfg))

	id, err := st.ResolveIDByName("x")
	assert.NilError(t, err)
	assert.Equal(t, id, "new-1")

	_, _, got, err := st.GetCommandConfig("x")
	assert.NilError(t, err)
	assert.DeepEqual(t, got.Argv, []string{"echo", "b"})

	state, _, _, err := st.GetCommandState("new-1")
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeCreated)

	_, err = st.ResolveID("old-1")
	assert.Assert(t, err != nil, "old command still resolves after replace")
	_, _, _, err = st.GetCommandState("old-1")
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "old state row: %v", err)
	history, err := st.GetExitHistory("old-1")
	assert.NilError(t, err)
	assert.Equal(t, len(history), 0)

	entries, err := st.ListCommands(true, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
}

func TestReplaceCommandKeepsOldOnInsertFailure(t *testing.T) {
	st := testStore(t)
	insertExitedCommand(t, st, "old-1", "x", "echo", "a")
	// new-1 is already taken, so inserting the replacement violates the
	// primary key after the old rows have been deleted inside the transaction.
	insertExitedCommand(t, st, "new-1", "y", "echo", "y")

	newCfg := replaceTestConfig("/tmp/cmd/new-1", "echo", "b")
	err := st.ReplaceCommand("old-1", "new-1", "x", newCfg)
	assert.ErrorContains(t, err, "insert config")

	id, err := st.ResolveIDByName("x")
	assert.NilError(t, err)
	assert.Equal(t, id, "old-1")

	_, _, got, err := st.GetCommandConfig("x")
	assert.NilError(t, err)
	assert.DeepEqual(t, got.Argv, []string{"echo", "a"})

	state, _, _, err := st.GetCommandState("old-1")
	assert.NilError(t, err)
	assert.Equal(t, state, model.EventTypeExited)

	history, err := st.GetExitHistory("old-1")
	assert.NilError(t, err)
	assert.Equal(t, len(history), 1)

	_, _, other, err := st.GetCommandConfig("y")
	assert.NilError(t, err)
	assert.DeepEqual(t, other.Argv, []string{"echo", "y"})
}

func TestResolveIDByNameMatchesOnlyNames(t *testing.T) {
	st := testStore(t)
	insertExitedCommand(t, st, "abcdef", "named", "true")

	id, err := st.ResolveIDByName("named")
	assert.NilError(t, err)
	assert.Equal(t, id, "abcdef")

	_, err = st.ResolveIDByName("abc")
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "id prefix: %v", err)
	_, err = st.ResolveIDByName("abcdef")
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "exact id: %v", err)
}
