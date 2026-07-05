package store

import (
	"sort"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"gotest.tools/v3/assert"
)

// seedLabelFixtures inserts a fixed set of commands with varied labels and states
// used to pin ListCommands/FindByLabels filtering behavior.
func seedLabelFixtures(t *testing.T, st *Store) {
	t.Helper()
	mk := func(id string, state model.EventType, labels map[string]string) {
		cfg := &model.CommandConfig{
			Argv:            []string{"/bin/true"},
			Dir:             "/tmp",
			Env:             testEnv(),
			RestartPolicy:   model.RestartPolicyNo,
			ScrollbackBytes: DefaultScrollbackBytes,
			LogDriver:       model.DefaultLogDriver,
			Labels:          labels,
			CommandDir:      "/tmp/cmd/" + id,
		}
		assert.NilError(t, st.InsertCommandConfig(id, id, cfg))
		assert.NilError(t, st.InsertCommandState(id, state, &model.CommandState{}))
	}
	mk("web", model.EventTypeRunning, map[string]string{"app": "web", "env": "prod"})
	mk("api", model.EventTypeRunning, map[string]string{"app": "api", "env": "prod"})
	mk("db", model.EventTypeExited, map[string]string{"app": "db", "env": "prod"})
	mk("bare", model.EventTypeRunning, nil)
	mk("quote", model.EventTypeRunning, map[string]string{`ns"x`: "v"})
}

func listIDs(t *testing.T, st *Store, allStates bool, labels map[string]string) []string {
	t.Helper()
	entries, err := st.ListCommands(allStates, labels)
	assert.NilError(t, err)
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	return ids
}

func findIDs(t *testing.T, st *Store, labels map[string]string) []string {
	t.Helper()
	raw, err := st.FindByLabels(labels)
	assert.NilError(t, err)
	ids := make([]string, 0, len(raw))
	ids = append(ids, raw...)
	sort.Strings(ids)
	return ids
}

func TestListCommandsLabelFilterParity(t *testing.T) {
	st := testStore(t)
	seedLabelFixtures(t, st)

	all := []string{"api", "bare", "db", "quote", "web"}

	// Empty label set matches every command (nil and empty map alike).
	assert.DeepEqual(t, listIDs(t, st, true, nil), all)
	assert.DeepEqual(t, listIDs(t, st, true, map[string]string{}), all)

	// A single label matches all rows carrying it.
	assert.DeepEqual(
		t,
		listIDs(t, st, true, map[string]string{"env": "prod"}),
		[]string{"api", "db", "web"},
	)

	// Multiple labels are ANDed together.
	assert.DeepEqual(
		t,
		listIDs(t, st, true, map[string]string{"app": "web", "env": "prod"}),
		[]string{"web"},
	)

	// A non-matching value excludes the row even if another label matches.
	assert.DeepEqual(
		t,
		listIDs(t, st, true, map[string]string{"app": "web", "env": "nope"}),
		[]string{},
	)
}

func TestListCommandsStateFilterParity(t *testing.T) {
	st := testStore(t)
	seedLabelFixtures(t, st)

	// allStates=true includes the exited "db".
	assert.DeepEqual(t, listIDs(t, st, true, nil), []string{"api", "bare", "db", "quote", "web"})

	// allStates=false keeps only created/starting/running, dropping "db".
	assert.DeepEqual(t, listIDs(t, st, false, nil), []string{"api", "bare", "quote", "web"})
}

func TestFindByLabelsParity(t *testing.T) {
	st := testStore(t)
	seedLabelFixtures(t, st)

	// Empty label set returns every command, ignoring state.
	assert.DeepEqual(t, findIDs(t, st, nil), []string{"api", "bare", "db", "quote", "web"})

	// Multiple labels are ANDed.
	assert.DeepEqual(
		t,
		findIDs(t, st, map[string]string{"app": "web", "env": "prod"}),
		[]string{"web"},
	)

	// A non-matching value yields no results.
	assert.DeepEqual(t, findIDs(t, st, map[string]string{"app": "web", "env": "nope"}), []string{})
}

// TestLabelKeyWithDoubleQuote pins the correct behavior for a label key that
// contains a double quote. This is a deliberate behavior FIX, not parity: the old
// json_extract path builder (labelJSONPath) could not quote such a key and matched
// nothing, so this test is red against the dynamic builder and green after the
// Option C json_each port (see label-query-options.md).
func TestLabelKeyWithDoubleQuote(t *testing.T) {
	st := testStore(t)
	seedLabelFixtures(t, st)

	assert.DeepEqual(t, listIDs(t, st, true, map[string]string{`ns"x`: "v"}), []string{"quote"})
	assert.DeepEqual(t, findIDs(t, st, map[string]string{`ns"x`: "v"}), []string{"quote"})
}

// TestListCommandsOrdersByCreatedAt pins the CreatedAt ordering guarantee. Rows
// are inserted out of CreatedAt order, then their CreatedAt stamps are set to
// known distinct values; ListCommands must return them ascending by CreatedAt.
func TestListCommandsOrdersByCreatedAt(t *testing.T) {
	st := testStore(t)
	for _, id := range []string{"c", "a", "b"} {
		cfg := &model.CommandConfig{
			Argv:            []string{"/bin/true"},
			Dir:             "/tmp",
			Env:             testEnv(),
			RestartPolicy:   model.RestartPolicyNo,
			ScrollbackBytes: DefaultScrollbackBytes,
			LogDriver:       model.DefaultLogDriver,
			CommandDir:      "/tmp/cmd/" + id,
		}
		assert.NilError(t, st.InsertCommandConfig(id, id, cfg))
		assert.NilError(t, st.InsertCommandState(id, model.EventTypeRunning, &model.CommandState{}))
	}
	stamps := map[string]string{
		"a": "2026-01-01T00:00:01Z",
		"b": "2026-01-01T00:00:02Z",
		"c": "2026-01-01T00:00:03Z",
	}
	for id, ts := range stamps {
		_, err := st.DB().Exec(`UPDATE CommandConfig SET CreatedAt = ? WHERE ID = ?`, ts, id)
		assert.NilError(t, err)
	}

	entries, err := st.ListCommands(true, nil)
	assert.NilError(t, err)
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.ID)
	}
	assert.DeepEqual(t, got, []string{"a", "b", "c"})
}
