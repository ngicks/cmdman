package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// setLastUsed backdates a history row so a following bump is observable despite
// RFC3339's one-second resolution.
func setLastUsed(t *testing.T, st *Store, workDir, project, lastUsed string) {
	t.Helper()
	_, err := st.DB().Exec(
		`UPDATE ComposeHistory SET LastUsed = ? WHERE WorkDir = ? AND Project = ?`,
		lastUsed, workDir, project,
	)
	assert.NilError(t, err)
}

func TestComposeHistoryRecordAndGet(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	assert.NilError(
		t,
		st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/cmd-compose.yaml"),
	)

	got, err := st.GetComposeHistory(ctx, "/work/app", "app")
	assert.NilError(t, err)
	assert.Equal(t, got.WorkDir, "/work/app")
	assert.Equal(t, got.Project, "app")
	assert.Equal(t, got.File, "/work/app/cmd-compose.yaml")

	ts, err := time.Parse(time.RFC3339, got.LastUsed)
	assert.NilError(t, err)
	_, offset := ts.Zone()
	assert.Equal(t, offset, 0, "LastUsed must be UTC: %s", got.LastUsed)

	_, err = st.GetComposeHistory(ctx, "/work/app", "other")
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "unrecorded project: %v", err)
}

// TestComposeHistoryUpsertOverwritesFile pins the "File is last-used, not part
// of the key" rule: re-recording the same project with another compose file
// replaces File in place rather than adding a row.
func TestComposeHistoryUpsertOverwritesFile(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/first.yaml"))
	setLastUsed(t, st, "/work/app", "app", "2020-01-01T00:00:00Z")
	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/second.yaml"))

	entries, err := st.ListComposeHistory(ctx)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
	assert.Equal(t, entries[0].File, "/work/app/second.yaml")
	assert.Assert(t, entries[0].LastUsed > "2020-01-01T00:00:00Z", "LastUsed not bumped: %s",
		entries[0].LastUsed)
}

// TestComposeHistoryKeyedOnWorkDirAndProject covers both key halves, including
// the unnamed project recorded as ”.
func TestComposeHistoryKeyedOnWorkDirAndProject(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/a.yaml"))
	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "", "/work/app/b.yaml"))
	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/other", "app", "/work/other/c.yaml"))

	entries, err := st.ListComposeHistory(ctx)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 3)

	unnamed, err := st.GetComposeHistory(ctx, "/work/app", "")
	assert.NilError(t, err)
	assert.Equal(t, unnamed.File, "/work/app/b.yaml")
}

func TestComposeHistoryTouch(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	assert.NilError(t, st.TouchComposeHistory(ctx, "/work/app", "app"))

	entries, err := st.ListComposeHistory(ctx)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 0, "touch must not insert a row")

	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/a.yaml"))
	setLastUsed(t, st, "/work/app", "app", "2020-01-01T00:00:00Z")

	assert.NilError(t, st.TouchComposeHistory(ctx, "/work/app", "app"))

	got, err := st.GetComposeHistory(ctx, "/work/app", "app")
	assert.NilError(t, err)
	assert.Equal(t, got.File, "/work/app/a.yaml", "touch must not clear File")
	assert.Assert(t, got.LastUsed > "2020-01-01T00:00:00Z", "LastUsed not bumped: %s", got.LastUsed)
}

// TestComposeHistoryDelete covers the stale-entry removal (D10/Q12): the row is
// gone and the rest of history is untouched, while forgetting a project that was
// never recorded reports "nothing removed" rather than failing — the launcher
// offers removal for discovered projects too.
func TestComposeHistoryDelete(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "app", "/work/app/a.yaml"))
	assert.NilError(t, st.RecordComposeHistory(ctx, "/work/app", "", "/work/app/b.yaml"))

	removed, err := st.DeleteComposeHistory(ctx, "/work/app", "app")
	assert.NilError(t, err)
	assert.Equal(t, removed, true)

	_, err = st.GetComposeHistory(ctx, "/work/app", "app")
	assert.Assert(t, errors.Is(err, sql.ErrNoRows), "forgotten project still there: %v", err)

	entries, err := st.ListComposeHistory(ctx)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1, "delete must key on the pair, not the work dir alone")
	assert.Equal(t, entries[0].Project, "")

	removed, err = st.DeleteComposeHistory(ctx, "/work/app", "app")
	assert.NilError(t, err)
	assert.Equal(t, removed, false)
}

// TestComposeHistoryListOrdersByRecency pins the launcher's expected order:
// most recently used first.
func TestComposeHistoryListOrdersByRecency(t *testing.T) {
	st := testStore(t)
	ctx := t.Context()

	for _, p := range []string{"old", "middle", "new"} {
		assert.NilError(t, st.RecordComposeHistory(ctx, "/work/"+p, p, "/work/"+p+"/c.yaml"))
	}
	setLastUsed(t, st, "/work/old", "old", "2020-01-01T00:00:00Z")
	setLastUsed(t, st, "/work/middle", "middle", "2022-01-01T00:00:00Z")
	setLastUsed(t, st, "/work/new", "new", "2024-01-01T00:00:00Z")

	entries, err := st.ListComposeHistory(ctx)
	assert.NilError(t, err)
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Project
	}
	assert.DeepEqual(t, got, []string{"new", "middle", "old"})
}
