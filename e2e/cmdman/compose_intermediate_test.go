package cmdman_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// composeProjectSummary returns the `compose ls` JSON summary of project in wd,
// or nil when it is not listed.
func composeProjectSummary(
	ctx context.Context,
	t *testing.T,
	e *testEnv,
	wd, project string,
) map[string]any {
	t.Helper()
	out := e.Cmd("compose", "ls", "--format", "json").Run(ctx, t)
	var summaries []map[string]any
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatalf("parse compose ls output: %v\n%s", err, out)
	}
	for _, s := range summaries {
		if s["Project"] == project && s["WorkDir"] == wd {
			return s
		}
	}
	return nil
}

// composePsJSON returns the `compose ps` JSON rows.
func composePsJSON(ctx context.Context, t *testing.T, ps *Cmd) []map[string]any {
	t.Helper()
	out := ps.Run(ctx, t)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse compose ps output: %v\n%s", err, out)
	}
	return rows
}

// TestComposeListsIntermediates checks that the compose listings show a
// resource holder under its project, while the verbs that act on the project's
// commands leave it alone.
func TestComposeListsIntermediates(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-intermediate-list"
	composePath := writeComposeFile(t, wd, composeResourceYAML(project, 1))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupResourceHolders(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	replica := composeReplica(ctx, env, wd, project, "web", 1)
	replicaID := replica["ID"].(string)
	replicaName := replica["Name"].(string)
	env.waitForState(ctx, replicaID, "running", defaultTimeout)

	compose("resource", "set", "web", "scratch", "/tmp/x").Run(ctx, t)
	holders := resourceHolders(ctx, env, wd, project)
	if len(holders) != 1 {
		t.Fatalf("want one holder, got %d: %v", len(holders), holders)
	}
	holderID := holders[0]["ID"].(string)
	holderName := holders[0]["Name"].(string)

	checkHolderRow := func(t *testing.T, row map[string]any) {
		t.Helper()
		if row["ID"] != holderID || row["Name"] != holderName ||
			row["Intermediate"] != "holder" || row["Owner"] != replicaName ||
			row["Command"] != "web" || row["State"] != "created" {
			t.Fatalf("unexpected holder row: %v", row)
		}
	}

	t.Run("compose ps lists the holder after its replica", func(t *testing.T) {
		rows := composePsJSON(ctx, t, compose("ps", "--format", "json"))
		if len(rows) != 2 {
			t.Fatalf("want the replica and the holder, got %d rows: %v", len(rows), rows)
		}
		if rows[0]["ID"] != replicaID {
			t.Fatalf("want the replica first, got %v", rows[0])
		}
		if _, ok := rows[0]["Intermediate"]; ok {
			t.Fatalf("a replica row has no kind: %v", rows[0])
		}
		checkHolderRow(t, rows[1])

		targeted := composePsJSON(ctx, t, compose("ps", "--format", "json", "web"))
		if len(targeted) != 2 {
			t.Fatalf("naming the service should keep its holder, got %v", targeted)
		}

		table := compose("ps").Run(ctx, t)
		lines := strings.Split(table, "\n")
		if header := strings.Fields(lines[0]); !slices.Contains(header, "KIND") ||
			!slices.Contains(header, "OWNER") {
			t.Fatalf("the table should have KIND and OWNER columns:\n%s", table)
		}
		var holderLine []string
		for _, line := range lines[1:] {
			if fields := strings.Fields(line); slices.Contains(fields, holderName) {
				holderLine = fields
			}
		}
		if !slices.Contains(holderLine, "holder") || !slices.Contains(holderLine, replicaName) {
			t.Fatalf("the holder row should show its kind and owner:\n%s", table)
		}
	})

	t.Run("compose ls counts the holder apart from the commands", func(t *testing.T) {
		summary := composeProjectSummary(ctx, t, env, wd, project)
		if summary == nil {
			t.Fatal("compose ls should list the project")
		}
		if summary["Commands"] != float64(1) || summary["Intermediates"] != float64(1) {
			t.Fatalf("want 1 command and 1 intermediate, got %v", summary)
		}
		if table := env.Cmd("compose", "ls").Run(ctx, t); !strings.Contains(
			strings.SplitN(table, "\n", 2)[0], "INTERMEDIATES") {
			t.Fatalf("the table should have an INTERMEDIATES column:\n%s", table)
		}
	})

	t.Run("compose stop leaves the holder alone", func(t *testing.T) {
		compose("stop").Run(ctx, t)
		if st := composeReplicaState(ctx, env, wd, project, "web", 1); !isStopped(st) {
			t.Fatalf("compose stop should stop the replica, got %q", st)
		}
		if st := env.inspectJSON(ctx, holderID)["State"]; st != "created" {
			t.Fatalf("compose stop should leave the holder alone, got state %v", st)
		}
	})

	t.Run("compose down leaves the holder alone", func(t *testing.T) {
		compose("down").Run(ctx, t)
		if left := env.lsJSON(ctx,
			"-l", "cmdman.compose.workdir="+wd,
			"-l", "cmdman.compose.project="+project,
		); len(left) != 0 {
			t.Fatalf("compose down should remove the replica, %d left", len(left))
		}
		if st := env.inspectJSON(ctx, holderID)["State"]; st != "created" {
			t.Fatalf("compose down should leave the holder alone, got state %v", st)
		}

		rows := composePsJSON(ctx, t, compose("ps", "--format", "json"))
		if len(rows) != 1 {
			t.Fatalf("want only the holder left, got %v", rows)
		}
		checkHolderRow(t, rows[0])

		summary := composeProjectSummary(ctx, t, env, wd, project)
		if summary == nil {
			t.Fatal("compose ls should keep listing a project that has a holder left")
		}
		if summary["Commands"] != float64(0) || summary["Intermediates"] != float64(1) {
			t.Fatalf("want no command and 1 intermediate, got %v", summary)
		}
	})
}
