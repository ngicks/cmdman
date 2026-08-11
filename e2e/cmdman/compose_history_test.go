package cmdman_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/cmdman/store"
)

// composeHistoryRow reads the ComposeHistory row for a project straight out of
// the store the CLI just wrote to. It fails the test when the row is absent.
func composeHistoryRow(
	ctx context.Context,
	t *testing.T,
	e *testEnv,
	workdir, project string,
) store.ComposeHistoryEntry {
	t.Helper()
	st, err := store.OpenStore(ctx, filepath.Join(e.dataHome, "commands.db"), true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	row, err := st.GetComposeHistory(ctx, workdir, project)
	if err != nil {
		t.Fatalf("get compose history for (%s, %s): %v", workdir, project, err)
	}
	return row
}

// TestComposeUpRecordsHistory verifies that `compose up` records the project in
// ComposeHistory and that re-upping with a different -f overwrites the recorded
// file while keeping the single (workdir, project) row.
func TestComposeUpRecordsHistory(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-history"
	first := writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	stdout, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", first, "up")
	if err != nil {
		t.Fatalf("compose up #1 failed: %v\nstdout:\n%s", err, stdout)
	}

	row := composeHistoryRow(ctx, t, env, wd, project)
	if row.File != first {
		t.Fatalf("history File: want %q, got %q", first, row.File)
	}
	if row.LastUsed == "" {
		t.Fatal("history LastUsed is empty")
	}

	// Tear the project down: its commands (and their file label) go away, but the
	// history row is exactly the state that must survive.
	if stdout, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", first, "down",
	); err != nil {
		t.Fatalf("compose down failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if row := composeHistoryRow(ctx, t, env, wd, project); row.File != first {
		t.Fatalf("history File after down: want %q, got %q", first, row.File)
	}

	// Bringing the same project up from another compose file overwrites File:
	// it is the last-used file, not part of the key.
	second := filepath.Join(wd, "other-compose.yaml")
	writeFile(t, second, composeBasicYAML(project))

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", second, "up")
	if err != nil {
		t.Fatalf("compose up #2 failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	row = composeHistoryRow(ctx, t, env, wd, project)
	if row.File != second {
		t.Fatalf("history File after re-up: want %q, got %q", second, row.File)
	}

	st, err := store.OpenStore(ctx, filepath.Join(env.dataHome, "commands.db"), true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	rows, err := st.ListComposeHistory(ctx)
	if err != nil {
		t.Fatalf("list compose history: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected a single history row for one project, got %#v", rows)
	}

	// An up on an unchanged project creates nothing, yet must still bump
	// recency. Backdating the row makes the bump observable regardless of how
	// close together the two ups run (LastUsed has one-second resolution).
	const backdated = "2020-01-01T00:00:00Z"
	if _, err := st.DB().Exec(
		`UPDATE ComposeHistory SET LastUsed = ? WHERE WorkDir = ? AND Project = ?`,
		backdated, wd, project,
	); err != nil {
		t.Fatalf("backdate history row: %v", err)
	}

	if stdout, stderr, err := env.exec(
		ctx, "compose", "--workdir", wd, "-f", second, "up",
	); err != nil {
		t.Fatalf("compose up #3 failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	row = composeHistoryRow(ctx, t, env, wd, project)
	if row.LastUsed == backdated {
		t.Fatal("an up on an unchanged project must still bump LastUsed")
	}
	if row.File != second {
		t.Fatalf("history File after unchanged up: want %q, got %q", second, row.File)
	}
}
