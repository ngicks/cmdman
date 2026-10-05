package cmdman_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.yaml.in/yaml/v4"
)

// canonicalHookExec is one event of a hook item in `compose config` output.
type canonicalHookExec struct {
	Args    []string `yaml:"args"`
	OnError string   `yaml:"on_error"`
}

// canonicalHooksConfig is a minimal view of `compose config` output that keeps
// only the hooks of each command.
type canonicalHooksConfig struct {
	Commands map[string]struct {
		Hooks []struct {
			Name       string             `yaml:"name"`
			Resource   string             `yaml:"resource"`
			CreatePre  *canonicalHookExec `yaml:"create_pre"`
			StartPost  *canonicalHookExec `yaml:"start_post"`
			StopPost   *canonicalHookExec `yaml:"stop_post"`
			RemovePost *canonicalHookExec `yaml:"remove_post"`
		} `yaml:"hooks"`
	} `yaml:"commands"`
}

// storedHook mirrors one element of the cmdman.compose.hooks label.
type storedHook struct {
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Events   map[string]struct {
		Args    []string `json:"args"`
		OnError string   `json:"on_error"`
	} `json:"events"`
}

// composeLifecycleHooksYAML declares one hooked command (app) and one plain
// command (plain). Both exit right away. notifyArg is the argument of app's
// start_post hook, so a test can edit a hook without touching anything else.
// The scratch resource is a directory made under wd.
func composeLifecycleHooksYAML(name, wd, notifyArg string) string {
	return fmt.Sprintf(`name: %s
commands:
  app:
    args: [sh, -c, "echo app"]
    hooks:
      - name: notify
        start_post: ["echo", %q]
        stop_post:
          args: ["echo", "app stopped"]
          on_error: ignore
      - name: scratch
        resource: scratch
        create_pre: ["mktemp", "-d", %q]
        remove_post: ["sh", "-c", "rm -rf \"$$CMDMAN_COMPOSE_RESOURCE_VALUE\""]
  plain:
    args: [sh, -c, "echo plain"]
`, name, notifyArg, filepath.Join(wd, "app.XXXX"))
}

func TestComposeLifecycleHooksConfig(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	composePath := writeComposeFile(t, wd,
		composeLifecycleHooksYAML("tc-hooks-config", wd, "app up"))

	stdout := env.Cmd("compose", "--workdir", wd, "-f", composePath, "config").Run(ctx, t)

	var got canonicalHooksConfig
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse config output: %v\noutput:\n%s", err, stdout)
	}
	hooks := got.Commands["app"].Hooks
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks on app, got %d; output:\n%s", len(hooks), stdout)
	}
	if len(got.Commands["plain"].Hooks) != 0 {
		t.Fatalf("plain must render no hooks; output:\n%s", stdout)
	}

	notify, scratch := hooks[0], hooks[1]
	if notify.Name != "notify" || notify.Resource != "" {
		t.Errorf("hooks[0] = %q (resource %q), want notify without a resource",
			notify.Name, notify.Resource)
	}
	// The argv form renders in the mapping form with the default on_error.
	assertHookExec(t, "notify.start_post", notify.StartPost, []string{"echo", "app up"}, "fail")
	assertHookExec(t, "notify.stop_post", notify.StopPost,
		[]string{"echo", "app stopped"}, "ignore")

	if scratch.Name != "scratch" || scratch.Resource != "scratch" {
		t.Errorf("hooks[1] = %q (resource %q), want scratch with resource scratch",
			scratch.Name, scratch.Resource)
	}
	assertHookExec(t, "scratch.create_pre", scratch.CreatePre,
		[]string{"mktemp", "-d", filepath.Join(wd, "app.XXXX")}, "fail")
	// $$ escapes compose interpolation and renders as a single $.
	assertHookExec(t, "scratch.remove_post", scratch.RemovePost,
		[]string{"sh", "-c", `rm -rf "$CMDMAN_COMPOSE_RESOURCE_VALUE"`}, "fail")
}

func assertHookExec(
	t *testing.T,
	what string,
	got *canonicalHookExec,
	wantArgs []string,
	wantOnError string,
) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is missing", what)
		return
	}
	if !slices.Equal(got.Args, wantArgs) {
		t.Errorf("%s.args = %q, want %q", what, got.Args, wantArgs)
	}
	if got.OnError != wantOnError {
		t.Errorf("%s.on_error = %q, want %q", what, got.OnError, wantOnError)
	}
}

func TestComposeLifecycleHooksInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hooks string
		want  string
	}{
		{
			name: "two acquires",
			hooks: `
      - name: scratch
        resource: scratch
        create_pre: ["true"]
        start_pre: ["true"]
`,
			want: "more than one acquire event",
		},
		{
			name: "stop_post on a create_pre resource",
			hooks: `
      - name: scratch
        resource: scratch
        create_pre: ["true"]
        stop_post: ["true"]
`,
			want: "stop_post cannot release a resource acquired at create_pre",
		},
		{
			name: "duplicate names",
			hooks: `
      - name: notify
        start_post: ["true"]
      - name: notify
        stop_post: ["true"]
`,
			want: `hook "notify": name is used by more than one hook`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			env := newTestEnv(t)
			wd := composeWorkdir(t)
			composePath := writeComposeFile(t, wd, `name: tc-hooks-invalid
commands:
  app:
    args: ["true"]
    hooks:
`+tc.hooks)

			env.Cmd("compose", "--workdir", wd, "-f", composePath, "config").
				ExpectFail(ctx, t, `command "app"`, tc.want)
		})
	}
}

func TestComposeLifecycleHooksLabelAndEnv(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-label"
	composePath := writeComposeFile(t, wd, composeLifecycleHooksYAML(project, wd, "app up"))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })

	env.Cmd("compose", "--workdir", wd, "-f", composePath, "create").Run(ctx, t)

	appID := composeCommandID(ctx, env, wd, project, "app")
	if appID == "" {
		t.Fatal("compose create did not create app")
	}
	cfg, _ := env.inspectJSON(ctx, appID)["Config"].(map[string]any)
	labels, _ := cfg["labels"].(map[string]any)
	raw, _ := labels["cmdman.compose.hooks"].(string)
	if raw == "" {
		t.Fatalf("app is missing the cmdman.compose.hooks label; labels: %v", labels)
	}
	var stored []storedHook
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("decode cmdman.compose.hooks %q: %v", raw, err)
	}
	if len(stored) != 2 || stored[0].Name != "notify" || stored[1].Name != "scratch" {
		t.Fatalf("stored hooks = %+v, want notify then scratch", stored)
	}
	if stored[1].Resource != "scratch" {
		t.Errorf("scratch resource = %q, want scratch", stored[1].Resource)
	}
	startPost := stored[0].Events["start_post"]
	if !slices.Equal(startPost.Args, []string{"echo", "app up"}) ||
		startPost.OnError != "fail" {
		t.Errorf("notify start_post = %+v, want args [echo app up] on_error fail", startPost)
	}
	if got := stored[0].Events["stop_post"].OnError; got != "ignore" {
		t.Errorf("notify stop_post on_error = %q, want ignore", got)
	}
	appEnv := env.configEnv(ctx, appID)
	if !slices.Contains(appEnv, "CMDMAN_COMPOSE_COMMAND=app") {
		t.Errorf("app env lacks CMDMAN_COMPOSE_COMMAND=app: %v", appEnv)
	}

	plainID := composeCommandID(ctx, env, wd, project, "plain")
	if plainID == "" {
		t.Fatal("compose create did not create plain")
	}
	plainCfg, _ := env.inspectJSON(ctx, plainID)["Config"].(map[string]any)
	plainLabels, _ := plainCfg["labels"].(map[string]any)
	if _, ok := plainLabels["cmdman.compose.hooks"]; ok {
		t.Errorf("plain must not carry cmdman.compose.hooks; labels: %v", plainLabels)
	}
	plainEnv := env.configEnv(ctx, plainID)
	if !slices.Contains(plainEnv, "CMDMAN_COMPOSE_COMMAND=plain") {
		t.Errorf("plain env lacks CMDMAN_COMPOSE_COMMAND=plain: %v", plainEnv)
	}
}

func TestComposeLifecycleHooksEditRecreates(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-recreate"
	composePath := writeComposeFile(t, wd, composeLifecycleHooksYAML(project, wd, "app up"))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })

	env.Cmd("compose", "--workdir", wd, "-f", composePath, "up").Run(ctx, t)

	// Wait for the short-running commands to exit so recreate isn't skipped.
	idsBefore := map[string]string{}
	for _, name := range []string{"app", "plain"} {
		id := composeCommandID(ctx, env, wd, project, name)
		if id == "" {
			t.Fatalf("compose up did not create %s", name)
		}
		env.waitForState(ctx, id, "exited", 5*time.Second)
		idsBefore[name] = id
	}

	writeComposeFile(t, wd, composeLifecycleHooksYAML(project, wd, "app is up"))

	stdout := env.Cmd("compose", "--workdir", wd, "-f", composePath, "up").Run(ctx, t)
	events := parseProgress(t, stdout)
	if !progressReached(events, "app", "recreated") {
		t.Fatalf("expected recreate for app after a hook edit; got:\n%s", stdout)
	}
	if !progressReached(events, "plain", "unchanged") {
		t.Fatalf("expected plain unchanged; got:\n%s", stdout)
	}

	if id := composeCommandID(ctx, env, wd, project, "app"); id == idsBefore["app"] {
		t.Errorf("app id should have changed across the recreate: %s", id)
	}
	if id := composeCommandID(ctx, env, wd, project, "plain"); id != idsBefore["plain"] {
		t.Errorf("plain id should be stable: was %s, now %s", idsBefore["plain"], id)
	}
}
