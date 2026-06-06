package cmdman_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInspect_FormatTemplate(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "fmt-tmpl", "--", "/bin/sh", "-c", "exit 5")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "fmt-tmpl", "exited", defaultTimeout)

	stdout := env.run(ctx, "inspect", "--format", "{{.Name}} {{.State}}", "fmt-tmpl")
	if stdout != "fmt-tmpl exited" {
		t.Errorf("expected %q, got %q", "fmt-tmpl exited", stdout)
	}
}

func TestInspect_FormatJSONFunc(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "fmt-json", "--", "/bin/sh", "-c", "echo done")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "fmt-json", "exited", defaultTimeout)

	stdout := env.run(ctx, "inspect", "--format", "{{json .Config.Argv}}", "fmt-json")
	var argv []string
	if err := json.Unmarshal([]byte(stdout), &argv); err != nil {
		t.Fatalf("parse json output: %v\nraw: %s", err, stdout)
	}
	if len(argv) < 3 || argv[0] != "/bin/sh" || argv[1] != "-c" || argv[2] != "echo done" {
		t.Errorf("unexpected argv: %v", argv)
	}
}

func TestInspect_DefaultStillJSON(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "fmt-default", "--", "/bin/sh", "-c", "echo done")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "fmt-default", "exited", defaultTimeout)

	stdout := env.run(ctx, "inspect", "fmt-default")
	// Pretty-printed JSON should contain a newline and the indented "ID" key.
	if !strings.Contains(stdout, "\n  \"ID\":") {
		t.Errorf("expected indented JSON output, got: %s", stdout)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("default output is not valid JSON: %v\nraw: %s", err, stdout)
	}
	if info["Name"] != "fmt-default" {
		t.Errorf("expected name=fmt-default, got %v", info["Name"])
	}
}

func TestInspect_BasicFields(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "inspect-me", "--", "/bin/sh", "-c", "echo inspect-test")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "inspect-me", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "inspect-me")

	// Check required top-level fields.
	if info["ID"] == nil || info["ID"] == "" {
		t.Error("inspect output missing 'ID'")
	}
	if info["Name"] != "inspect-me" {
		t.Errorf("expected name=inspect-me, got %v", info["Name"])
	}
	if info["State"] != "exited" {
		t.Errorf("expected state=exited, got %v", info["State"])
	}
	if info["Config"] == nil {
		t.Error("inspect output missing 'Config'")
	}
	if info["StateJSON"] == nil {
		t.Error("inspect output missing 'StateJSON'")
	}
	if info["ConfigPath"] == nil || info["ConfigPath"] == "" {
		t.Error("inspect output missing 'ConfigPath'")
	}
}

func TestInspect_ConfigContainsArgv(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo argv-check")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["Config"].(map[string]any)
	argv, _ := cfg["argv"].([]any)

	if len(argv) < 3 {
		t.Fatalf("expected at least 3 argv elements, got %v", argv)
	}
	if argv[0] != "/bin/sh" {
		t.Errorf("expected argv[0]=/bin/sh, got %v", argv[0])
	}
	if argv[1] != "-c" {
		t.Errorf("expected argv[1]=-c, got %v", argv[1])
	}
	if argv[2] != "echo argv-check" {
		t.Errorf("expected argv[2]='echo argv-check', got %v", argv[2])
	}
}

func TestInspect_StateDetailHasTimestamps(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "echo ts-check")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	stateDetail, _ := info["StateJSON"].(map[string]any)

	startedAt, _ := stateDetail["started_at"].(string)
	if startedAt == "" {
		t.Error("expected started_at timestamp in state_detail")
	}

	finishedAt, _ := stateDetail["finished_at"].(string)
	if finishedAt == "" {
		t.Error("expected finished_at timestamp in state_detail")
	}
}

func TestInspect_ExitHistory(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "--", "/bin/sh", "-c", "exit 7")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	history, _ := info["ExitHistory"].([]any)

	if len(history) == 0 {
		t.Fatal("expected at least one exit_history entry")
	}

	firstExit, _ := history[0].(map[string]any)
	exitCode, _ := firstExit["ExitCode"].(float64)
	if exitCode != 7 {
		t.Errorf("expected exit_code=7 in exit_history, got %v", exitCode)
	}
	ts, _ := firstExit["Timestamp"].(string)
	if ts == "" {
		t.Error("expected timestamp in exit_history entry")
	}
}

func TestInspect_ByNameAndByID(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "lookup-test", "--", "/bin/sh", "-c", "echo lookup")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	// Inspect by name.
	byName := env.inspectJSON(ctx, "lookup-test")
	// Inspect by ID.
	byID := env.inspectJSON(ctx, id)

	// Both should return the same id.
	if byName["ID"] != byID["ID"] {
		t.Errorf(
			"inspect by name and by ID returned different IDs: %v vs %v",
			byName["ID"],
			byID["ID"],
		)
	}
}

func TestInspect_LiveStatusForRunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "live-status", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "live-status", "running", defaultTimeout)

	info := env.inspectJSON(ctx, "live-status")

	// A running command should have live_status populated.
	liveStatus, _ := info["LiveStatus"].(map[string]any)
	if liveStatus == nil {
		t.Fatal("expected live_status for running command")
	}
	if liveStatus["State"] != "running" {
		t.Errorf("expected live_status.state=running, got %v", liveStatus["State"])
	}
	pid, _ := liveStatus["PID"].(float64)
	if pid <= 0 {
		t.Errorf("expected live_status.pid > 0, got %v", pid)
	}
}

func TestInspect_LabelsInConfig(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run",
		"-l", "app=web",
		"-l", "env=staging",
		"--", "/bin/sh", "-c", "echo labeled",
	)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, id, "exited", defaultTimeout)

	info := env.inspectJSON(ctx, id)
	cfg, _ := info["Config"].(map[string]any)
	labels, _ := cfg["labels"].(map[string]any)

	if labels["app"] != "web" {
		t.Errorf("expected label app=web, got %v", labels["app"])
	}
	if labels["env"] != "staging" {
		t.Errorf("expected label env=staging, got %v", labels["env"])
	}
}
