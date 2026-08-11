package cmdman_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigCommandJSON checks the default output: indented JSON keyed by the
// snake_case config-file keys, carrying the directories this invocation
// resolved. DefaultEnvironment is tagged json:"-", so the process environment
// must not be dumped here.
func TestConfigCommandJSON(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	stdout := env.run(ctx, "config")

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse config output: %v\nraw output:\n%s", err, stdout)
	}

	if got["data_dir"] != env.dataHome {
		t.Errorf("data_dir = %v, want %q", got["data_dir"], env.dataHome)
	}
	if got["runtime_dir"] != env.runtimeDir {
		t.Errorf("runtime_dir = %v, want %q", got["runtime_dir"], env.runtimeDir)
	}
	for _, key := range []string{
		"default_working_dir",
		"default_scrollback_bytes",
		"default_log_driver",
		"event_watcher_kind",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q; got keys %v", key, got)
		}
	}
	if _, ok := got["DefaultEnvironment"]; ok {
		t.Errorf("DefaultEnvironment leaked into JSON output:\n%s", stdout)
	}

	// Indented, not the compact form the {{json .}} helper produces.
	if !strings.Contains(stdout, "\n  \"data_dir\"") {
		t.Errorf("output is not indented JSON:\n%s", stdout)
	}
}

// TestConfigCommandFormat covers --format: a Go template against the Go field
// names, including a field that the default JSON output omits.
func TestConfigCommandFormat(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	if got := env.run(ctx, "config", "--format", "{{.DataDir}}"); got != env.dataHome {
		t.Errorf("--format {{.DataDir}} = %q, want %q", got, env.dataHome)
	}
	// -f is the shorthand, and the shared json helper stays compact/one line.
	got := env.run(ctx, "config", "-f", "{{.RuntimeDir}} {{ json .DefaultLogDriver }}")
	want := env.runtimeDir + ` "k8s-file"`
	if got != want {
		t.Errorf("config -f = %q, want %q", got, want)
	}
	// DefaultEnvironment is json:"-" yet still reachable from a template.
	if got := env.run(ctx, "config", "-f", "{{len .DefaultEnvironment}}"); got == "0" {
		t.Errorf("DefaultEnvironment is empty; got %q", got)
	}
}

// TestConfigCommandReflectsRootFlags pins the deliberate deviation from the
// scaffold template: runConfig goes through the shared loadConfig, so the
// explicitly-set root flags win over the environment and the printed config is
// the one a sibling command with the same flags would use.
func TestConfigCommandReflectsRootFlags(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	overridden := t.TempDir()
	got := env.run(ctx, "--data-dir", overridden, "config", "-f", "{{.DataDir}}")
	if got != overridden {
		t.Errorf("--data-dir override = %q, want %q", got, overridden)
	}

	// The flag is scoped to the field it names; the rest still comes from the
	// lower layers.
	got = env.run(ctx, "--data-dir", overridden, "config", "-f", "{{.RuntimeDir}}")
	if got != env.runtimeDir {
		t.Errorf("runtime_dir = %q, want %q", got, env.runtimeDir)
	}
}

// TestConfigCommandLayersConfigFile covers the middle layer the other cases
// cannot see: newTestEnv points $CMDMAN_CONF at a file that does not exist, so
// only an explicit --config exercises it. DefaultScrollbackBytes has no env tag,
// which makes it the field that isolates the file layer.
func TestConfigCommandLayersConfigFile(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	confPath := filepath.Join(t.TempDir(), "config.json")
	must(t, os.WriteFile(confPath, []byte(`{"default_scrollback_bytes": 4096}`), 0o644))

	got := env.run(ctx, "--config", confPath, "config", "-f", "{{.DefaultScrollbackBytes}}")
	if got != "4096" {
		t.Errorf("default_scrollback_bytes = %q, want %q", got, "4096")
	}

	// Keys the file leaves out still come from the layer below - here the
	// environment newTestEnv sets.
	got = env.run(ctx, "--config", confPath, "config", "-f", "{{.DataDir}}")
	if got != env.dataHome {
		t.Errorf("data_dir = %q, want %q", got, env.dataHome)
	}
}

// TestConfigCommandBadTemplate checks that a malformed --format is reported as
// an error rather than printed.
func TestConfigCommandBadTemplate(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	stdout, stderr := env.runExpectFail(ctx, "config", "--format", "{{.DataDir")
	if !strings.Contains(stderr, "parse format template") {
		t.Errorf("stderr = %q, want it to mention the template parse failure", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}

	// A well-formed template naming a field Config does not have fails at
	// execution instead.
	_, stderr = env.runExpectFail(ctx, "config", "--format", "{{.NoSuchField}}")
	if !strings.Contains(stderr, "execute format template") {
		t.Errorf("stderr = %q, want it to mention the template execution failure", stderr)
	}
}

// TestConfigCommandRejectsArgs keeps the command Args: cobra.NoArgs.
func TestConfigCommandRejectsArgs(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "config", "extra")
	if !strings.Contains(stderr, "unknown command") &&
		!strings.Contains(stderr, "accepts 0 arg") {
		t.Errorf("stderr = %q, want an argument rejection", stderr)
	}
}
