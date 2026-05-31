package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v4"
)

// canonicalConfig is a minimal view of the `compose config` YAML output, used to
// assert that values are fully resolved.
type canonicalConfig struct {
	Name     string `yaml:"name"`
	WorkDir  string `yaml:"work_dir"`
	Commands map[string]struct {
		Dir           string   `yaml:"dir"`
		Args          []string `yaml:"args"`
		Env           []string `yaml:"env"`
		RestartPolicy string   `yaml:"restart_policy"`
		After         map[string]struct {
			Condition string `yaml:"condition"`
		} `yaml:"after"`
	} `yaml:"commands"`
}

func TestComposeConfigResolvesAndRenders(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-config"

	// Interpolation (${GREETING}/${PORT}), env: layering, restart_policy with a
	// retry cap, and an after dependency all exercise the resolution path.
	composeYAML := fmt.Sprintf(`name: %s
commands:
  web:
    args: [sh, -c, "echo ${GREETING} on ${PORT}"]
    env:
      - PORT=8080
      - GREETING=hello
    restart_policy: on-failure:2
    after:
      db:
        condition: completed_successfully
  db:
    args: [sh, -c, "echo db"]
`, project)
	writeComposeFile(t, wd, composeYAML)

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd,
		"-f", filepath.Join(wd, "cmd-compose.yaml"), "config")
	if err != nil {
		t.Fatalf("compose config failed: %v\nstderr:\n%s", err, stderr)
	}

	var got canonicalConfig
	if err := yaml.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("parse config output: %v\noutput:\n%s", err, stdout)
	}

	if got.Name != project {
		t.Errorf("name = %q, want %q", got.Name, project)
	}
	if got.WorkDir != wd {
		t.Errorf("work_dir = %q, want %q", got.WorkDir, wd)
	}

	web, ok := got.Commands["web"]
	if !ok {
		t.Fatalf("missing web command in output:\n%s", stdout)
	}
	// Args interpolation: ${GREETING} and ${PORT} resolved from env:.
	wantArgs := []string{"sh", "-c", "echo hello on 8080"}
	if fmt.Sprint(web.Args) != fmt.Sprint(wantArgs) {
		t.Errorf("web.args = %v, want %v", web.Args, wantArgs)
	}
	// Env is merged and sorted (GREETING before PORT).
	wantEnv := []string{"GREETING=hello", "PORT=8080"}
	if fmt.Sprint(web.Env) != fmt.Sprint(wantEnv) {
		t.Errorf("web.env = %v, want %v", web.Env, wantEnv)
	}
	if web.RestartPolicy != "on-failure:2" {
		t.Errorf("web.restart_policy = %q, want %q", web.RestartPolicy, "on-failure:2")
	}
	if web.Dir != wd {
		t.Errorf("web.dir = %q, want %q (default to work_dir)", web.Dir, wd)
	}
	if cond := web.After["db"].Condition; cond != "completed_successfully" {
		t.Errorf("web.after.db.condition = %q, want %q", cond, "completed_successfully")
	}

	if _, ok := got.Commands["db"]; !ok {
		t.Fatalf("missing db command in output:\n%s", stdout)
	}
}

func TestComposeConfigInvalidFileErrors(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)

	// A compose file with no project name and no --project-name is invalid.
	writeComposeFile(t, wd, "commands:\n  a:\n    args: [true]\n")

	_, _, err := env.exec(ctx, "compose", "--workdir", wd,
		"-f", filepath.Join(wd, "cmd-compose.yaml"), "config")
	if err == nil {
		t.Fatalf("expected compose config to fail for a file without a project name")
	}
}
