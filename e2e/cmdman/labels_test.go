package cmdman_test

import (
	"strings"
	"testing"
)

func TestLabels_MultipleLabelsANDLogic(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Create commands with different label combinations.
	id1 := env.run(ctx, "run", "-l", "env=prod", "-l", "tier=web", "--", "/bin/sh", "-c", "sleep 300")
	id2 := env.run(ctx, "run", "-l", "env=prod", "-l", "tier=api", "--", "/bin/sh", "-c", "sleep 300")
	id3 := env.run(ctx, "run", "-l", "env=staging", "-l", "tier=web", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
		env.cleanupCommand(ctx, id3)
	})

	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)
	env.waitForState(ctx, id3, "running", defaultTimeout)

	// Filter by both labels (AND logic): env=prod AND tier=web.
	stdout := env.run(ctx, "ls", "-q", "-l", "env=prod", "-l", "tier=web")
	if !strings.Contains(stdout, id1) {
		t.Errorf("expected %s (env=prod, tier=web) in output", id1)
	}
	if strings.Contains(stdout, id2) {
		t.Errorf("did not expect %s (env=prod, tier=api) in output", id2)
	}
	if strings.Contains(stdout, id3) {
		t.Errorf("did not expect %s (env=staging, tier=web) in output", id3)
	}
}

func TestLabels_SingleLabel(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(ctx, "run", "-l", "role=worker", "--", "/bin/sh", "-c", "sleep 300")
	id2 := env.run(ctx, "run", "-l", "role=scheduler", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() {
		env.cleanupCommand(ctx, id1)
		env.cleanupCommand(ctx, id2)
	})

	env.waitForState(ctx, id1, "running", defaultTimeout)
	env.waitForState(ctx, id2, "running", defaultTimeout)

	// Filter by single label.
	stdout := env.run(ctx, "ls", "-q", "-l", "role=worker")
	if !strings.Contains(stdout, id1) {
		t.Errorf("expected %s in output for role=worker", id1)
	}
	if strings.Contains(stdout, id2) {
		t.Errorf("did not expect %s in output for role=worker", id2)
	}
}

func TestLabels_NoMatch(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-l", "color=blue", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "running", defaultTimeout)

	// Filter by a non-existent label value.
	entries := env.lsJSON(ctx, "-l", "color=red")
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for color=red, got %d", len(entries))
	}
}

func TestLabels_RmByLabel(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id1 := env.run(ctx, "run", "-l", "disposable=yes", "--", "/bin/sh", "-c", "echo a")
	id2 := env.run(ctx, "run", "-l", "disposable=yes", "--", "/bin/sh", "-c", "echo b")
	id3 := env.run(ctx, "run", "-l", "disposable=no", "--", "/bin/sh", "-c", "echo c")

	env.waitForState(ctx, id1, "exited", defaultTimeout)
	env.waitForState(ctx, id2, "exited", defaultTimeout)
	env.waitForState(ctx, id3, "exited", defaultTimeout)

	// Remove only disposable=yes.
	env.run(ctx, "rm", "-l", "disposable=yes")

	// Only id3 should remain.
	entries := env.lsJSON(ctx)
	for _, e := range entries {
		eid, _ := e["ID"].(string)
		if eid == id1 || eid == id2 {
			t.Errorf("labeled command %s still present after rm -l", eid)
		}
	}

	// Clean up the remaining one.
	env.run(ctx, "rm", id3)
}

func TestLabels_InspectShowsLabels(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run",
		"-l", "service=api",
		"-l", "version=v2",
		"--", "/bin/sh", "-c", "echo labeled-inspect",
	)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["config"].(map[string]any)
	labels, _ := cfg["labels"].(map[string]any)

	if labels["service"] != "api" {
		t.Errorf("expected label service=api, got %v", labels["service"])
	}
	if labels["version"] != "v2" {
		t.Errorf("expected label version=v2, got %v", labels["version"])
	}
}
