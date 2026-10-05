package cmdman_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tests below drive the hooks of compose stop, down and restart through
// the CLI, with the mark hooks of compose_hooks_run_test.go.

// commandNames lists the name of every command of the test's store.
func commandNames(ctx context.Context, t *testing.T, e *testEnv) []string {
	t.Helper()
	return strings.Fields(e.Cmd("ls", "--all", "--format", "{{.Name}}").Run(ctx, t))
}

// fileLines returns the lines of path, sorted, or nil when it does not exist.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	must(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	slices.Sort(lines)
	return lines
}

// progressErrorWith reports whether any event of the trace carries an error
// containing want.
func progressErrorWith(events []progressEvent, want string) bool {
	return slices.ContainsFunc(events, func(ev progressEvent) bool {
		return strings.Contains(ev.Error, want)
	})
}

func TestComposeHooksDownRunsTeardownHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-down"
	marker := filepath.Join(wd, "marker.txt")
	released := filepath.Join(wd, "released.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 2)+fmt.Sprintf(
		`      - name: scratch
        resource: scratch
        create_pre: [sh, -c, "echo scratch-$$CMDMAN_COMPOSE_SCALE_INDEX"]
        remove_post: [sh, -c, %q]
`, `echo "$$CMDMAN_COMPOSE_SCALE_INDEX $$CMDMAN_COMPOSE_RESOURCE_VALUE" >> `+
			shellQuote(released)))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	for idx := 1; idx <= 2; idx++ {
		env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", idx), "running",
			defaultTimeout)
	}
	clearMarker(t, marker)

	compose("down").Run(ctx, t)

	want := []string{"v1 stop_pre", "v1 stop_post", "v1 remove_pre", "v1 remove_post"}
	for idx := 1; idx <= 2; idx++ {
		if got := markedEvents(t, marker, "web", idx); !slices.Equal(got, want) {
			t.Errorf("hooks of web-%d = %q, want %q", idx, got, want)
		}
	}
	if got, want := fileLines(t, released), []string{"1 scratch-1", "2 scratch-2"}; !slices.Equal(
		got, want) {
		t.Errorf("released = %q, want %q", got, want)
	}
	if names := commandNames(ctx, t, env); len(names) != 0 {
		t.Errorf("down should leave no command, holder or hook command behind: %q", names)
	}
}

func TestComposeHooksStopReleasesStartResource(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-stop"
	marker := filepath.Join(wd, "marker.txt")
	counter := filepath.Join(wd, "counter.txt")
	released := filepath.Join(wd, "released.txt")
	acquire := fmt.Sprintf(`n=$$(cat %[1]s 2>/dev/null || echo 0); n=$$((n+1)); `+
		`echo $$n > %[1]s; echo port-$$n`, shellQuote(counter))
	release := `echo "$$CMDMAN_COMPOSE_RESOURCE_VALUE" >> ` + shellQuote(released)
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 1)+fmt.Sprintf(
		`      - name: port
        resource: port
        start_pre: [sh, -c, %q]
        stop_post: [sh, -c, %q]
`, acquire, release))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 1), "running",
		defaultTimeout)
	if got := compose("resource", "get", "web", "port").Run(ctx, t); got != "port-1" {
		t.Fatalf("resource after up = %q, want port-1", got)
	}
	clearMarker(t, marker)

	compose("stop").Run(ctx, t)

	if got, want := markedEvents(t, marker, "web", 1), []string{
		"v1 stop_pre", "v1 stop_post",
	}; !slices.Equal(got, want) {
		t.Errorf("hooks of the stop = %q, want %q", got, want)
	}
	if got := fileLines(t, released); !slices.Equal(got, []string{"port-1"}) {
		t.Errorf("released = %q, want [port-1]", got)
	}
	compose("resource", "get", "web", "port").ExpectFail(ctx, t)

	compose("start").Run(ctx, t)

	if got := compose("resource", "get", "web", "port").Run(ctx, t); got != "port-2" {
		t.Errorf("resource after start = %q, want port-2", got)
	}
}

func TestComposeHooksDownReleasesStartResourceOfExitedReplica(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-down-exited"
	released := filepath.Join(wd, "released.txt")
	release := `echo "$$CMDMAN_COMPOSE_RESOURCE_VALUE" >> ` + shellQuote(released)
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: ["true"]
    hooks:
      - name: port
        resource: port
        start_pre: [echo, port-1]
        stop_post: [sh, -c, %q]
`, project, release))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 1), "exited",
		defaultTimeout)
	if got := compose("resource", "get", "web", "port").Run(ctx, t); got != "port-1" {
		t.Fatalf("resource after up = %q, want port-1", got)
	}

	// web exited on its own, so down runs no stop hooks for it.
	stdout := compose("down", "--progress", "json").Run(ctx, t)

	if got := fileLines(t, released); !slices.Equal(got, []string{"port-1"}) {
		t.Errorf("released = %q, want [port-1]", got)
	}
	if !hasHookRecord(hookProgress(t, stdout), "web", "port", "stop_post", "hook-succeeded") {
		t.Errorf("down should report the stop_post release:\n%s", stdout)
	}
	if names := commandNames(ctx, t, env); len(names) != 0 {
		t.Errorf("down should leave no command or holder behind: %q", names)
	}
}

func TestComposeHooksRestartRunsStopThenStartHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-restart"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 1))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 1), "running",
		defaultTimeout)
	clearMarker(t, marker)

	compose("restart").Run(ctx, t)

	want := []string{"v1 stop_pre", "v1 stop_post", "v1 start_pre", "v1 start_post"}
	if got := markedEvents(t, marker, "web", 1); !slices.Equal(got, want) {
		t.Fatalf("hooks of the restart = %q, want %q", got, want)
	}
}

func TestComposeHooksRestartProgress(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-restart-progress"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 1)+
		`      - name: warn
        stop_pre:
          args: [sh, -c, "exit 1"]
          on_error: continue
`)
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 1), "running",
		defaultTimeout)

	stdout := compose("restart", "--progress", "json").Run(ctx, t)

	events := hookProgress(t, stdout)
	for _, want := range []struct{ hook, lifecycle, phase string }{
		{"mark", "stop_pre", "hook-succeeded"},
		{"mark", "stop_post", "hook-succeeded"},
		{"mark", "start_pre", "hook-running"},
		{"mark", "start_post", "hook-succeeded"},
		{"warn", "stop_pre", "hook-warning"},
	} {
		if !hasHookRecord(events, "web", want.hook, want.lifecycle, want.phase) {
			t.Errorf("no %s record of %s %s:\n%s", want.phase, want.hook, want.lifecycle, stdout)
		}
	}
	for _, ev := range parseProgress(t, stdout) {
		if ev.Op != "restart" {
			t.Errorf("a restart record names op %q:\n%s", ev.Op, stdout)
			break
		}
	}
	if !hasResultLine(stdout, "restarted", "web") {
		t.Errorf("restart should still print its result line:\n%s", stdout)
	}
}

func TestComposeHooksDownKeepsReplicaWhoseStopPreFails(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-down-keep"
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    scale: 2
    args: [sleep, "300"]
    hooks:
      - name: gate
        stop_pre: [sh, -c, '[ "$$CMDMAN_COMPOSE_SCALE_INDEX" != 1 ]']
  db:
    args: [sleep, "300"]
`, project))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	keptID := replicaID(ctx, t, env, wd, project, "web", 1)
	env.waitForState(ctx, keptID, "running", defaultTimeout)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 2), "running",
		defaultTimeout)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "db", 1), "running",
		defaultTimeout)

	res := compose("down", "--progress", "json").ExpectFail(ctx, t,
		"compose down operation(s) failed")

	if status := exitStatusOf(t, res.Err); status != 1 {
		t.Errorf("down exited %d, want 1", status)
	}
	if st := env.inspectJSON(ctx, keptID)["State"]; st != "running" {
		t.Errorf("the replica whose stop_pre failed should keep running, got %v", st)
	}
	events := parseProgress(t, res.Stdout)
	if !progressReached(events, "web-1", "skipped") ||
		!progressErrorWith(events, "kept after a failed stop hook") {
		t.Errorf("web-1 should be reported kept:\n%s", res.Stdout)
	}
	if composeReplica(ctx, env, wd, project, "web", 2) != nil {
		t.Errorf("web-2 should be removed despite the failure of web-1")
	}
	if composeReplica(ctx, env, wd, project, "db", 1) != nil {
		t.Errorf("db should be removed despite the failure of web-1")
	}

	compose("down", "--force").Run(ctx, t)

	if names := commandNames(ctx, t, env); len(names) != 0 {
		t.Errorf("down --force should leave nothing behind: %q", names)
	}
}

// replaceHooksLabel replaces the stopped command name with a copy whose
// cmdman.compose.hooks label is hooks, every other label kept, and starts it.
func replaceHooksLabel(ctx context.Context, t *testing.T, e *testEnv, name, hooks string) {
	t.Helper()
	cfg, _ := e.inspectJSON(ctx, name)["Config"].(map[string]any)
	dir, _ := cfg["dir"].(string)
	labels, _ := cfg["labels"].(map[string]any)
	args := []string{"create", "--replace", "-n", name, "-w", dir}
	for k, v := range labels {
		if k != "cmdman.compose.hooks" {
			args = append(args, "-l", fmt.Sprintf("%s=%v", k, v))
		}
	}
	args = append(args, "-l", "cmdman.compose.hooks="+hooks, "--")
	for _, a := range cfg["argv"].([]any) {
		args = append(args, a.(string))
	}
	e.Cmd(args...).Run(ctx, t)
	e.Cmd("start", name).Run(ctx, t)
	e.waitForState(ctx, name, "running", defaultTimeout)
}

func TestComposeHooksDownForceTearsDownUndecodableHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-down-undecodable"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 1))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	compose("up").Run(ctx, t)
	name := composeReplica(ctx, env, wd, project, "web", 1)["Name"].(string)
	env.waitForState(ctx, name, "running", defaultTimeout)
	env.Cmd("stop", name).Run(ctx, t)
	replaceHooksLabel(ctx, t, env, name, "not-json")
	clearMarker(t, marker)

	res := compose("down", "--progress", "json").ExpectFail(ctx, t,
		"compose down operation(s) failed")

	if !progressErrorWith(parseProgress(t, res.Stdout), "decode cmdman.compose.hooks") {
		t.Errorf("down should report the hooks it cannot decode:\n%s", res.Stdout)
	}
	if st := composeReplicaState(ctx, env, wd, project, "web", 1); st != "running" {
		t.Errorf("down without --force should keep the replica running, got %q", st)
	}

	stdout := compose("down", "--force", "--progress", "json").Run(ctx, t)

	events := parseProgress(t, stdout)
	if !progressReached(events, "web", "hook-warning") ||
		!progressErrorWith(events, "decode cmdman.compose.hooks") {
		t.Errorf("down --force should warn about the hooks it cannot decode:\n%s", stdout)
	}
	if !progressReached(events, "web", "removed") {
		t.Errorf("down --force should remove web:\n%s", stdout)
	}
	if got := markedEvents(t, marker, "web", 1); len(got) != 0 {
		t.Errorf("no hook should run for undecodable hooks, ran %q", got)
	}
	if names := commandNames(ctx, t, env); len(names) != 0 {
		t.Errorf("down --force should leave nothing behind: %q", names)
	}
}

// releaseRetryYAML declares web with a scratch resource whose release fails
// until the file ok exists, and appends the value it releases to released.
func releaseRetryYAML(project, ok, released string) string {
	release := fmt.Sprintf(`test -e %s && echo "$$CMDMAN_COMPOSE_RESOURCE_VALUE" >> %s`,
		shellQuote(ok), shellQuote(released))
	return fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    hooks:
      - name: scratch
        resource: scratch
        create_pre: [echo, scratch-value]
        remove_post: [sh, -c, %q]
`, project, release)
}

func TestComposeHooksDownRetriesFailedRelease(t *testing.T) {
	t.Parallel()
	for _, fileless := range []bool{false, true} {
		t.Run(fmt.Sprintf("fileless=%t", fileless), func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)
			env := newTestEnv(t)
			wd := composeWorkdir(t)
			project := "tc-hooks-release-retry"
			ok := filepath.Join(wd, "ok")
			released := filepath.Join(wd, "released.txt")
			composePath := writeComposeFile(t, wd, releaseRetryYAML(project, ok, released))
			t.Cleanup(func() {
				ctx := context.Background()
				cleanupProject(ctx, env, wd, project)
				cleanupIntermediates(ctx, env, wd, project)
			})
			compose := func(args ...string) *Cmd {
				return env.Cmd(append(
					[]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
			}

			compose("up").Run(ctx, t)
			replica := composeReplica(ctx, env, wd, project, "web", 1)["Name"].(string)

			res := compose("down", "--progress", "json").ExpectFail(ctx, t)

			if !progressErrorWith(parseProgress(t, res.Stdout), `value "scratch-value"`) {
				t.Errorf("the error should name the value the release keeps:\n%s", res.Stdout)
			}
			if composeReplica(ctx, env, wd, project, "web", 1) != nil {
				t.Errorf("the replica should be removed before remove_post")
			}
			holder := replica + ".res.scratch"
			if names := commandNames(ctx, t, env); !slices.Equal(names, []string{holder}) {
				t.Fatalf("only the holder %s should be left, got %q", holder, names)
			}

			must(t, os.WriteFile(ok, nil, 0o644))
			retry := compose("down")
			if fileless {
				must(t, os.Remove(composePath))
				retry = env.Cmd("compose", "--workdir", wd, "-p", project, "down").
					InDir(t.TempDir())
			}
			retry.Run(ctx, t)

			if got := fileLines(t, released); !slices.Equal(got, []string{"scratch-value"}) {
				t.Errorf("released = %q, want [scratch-value]", got)
			}
			if names := commandNames(ctx, t, env); len(names) != 0 {
				t.Errorf("the retried release should remove the holder: %q", names)
			}
		})
	}
}

func TestComposeHooksDownReleaseOnError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		onError  string
		wantHeld bool
		phase    string
	}{
		{onError: "continue", wantHeld: true, phase: "hook-warning"},
		{onError: "ignore", wantHeld: false, phase: "hook-ignored"},
	} {
		t.Run(tc.onError, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)
			env := newTestEnv(t)
			wd := composeWorkdir(t)
			project := "tc-hooks-release-" + tc.onError
			composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
    hooks:
      - name: scratch
        resource: scratch
        create_pre: [echo, scratch-value]
        remove_post:
          args: [sh, -c, "exit 1"]
          on_error: %s
`, project, tc.onError))
			t.Cleanup(func() {
				ctx := context.Background()
				cleanupProject(ctx, env, wd, project)
				cleanupIntermediates(ctx, env, wd, project)
			})
			compose := func(args ...string) *Cmd {
				return env.Cmd(append(
					[]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
			}

			compose("up").Run(ctx, t)

			stdout := compose("down", "--progress", "json").Run(ctx, t)

			if !slices.ContainsFunc(hookProgress(t, stdout), func(ev hookProgressEvent) bool {
				return ev.Hook == "scratch" && ev.Lifecycle == "remove_post" &&
					ev.Phase == tc.phase
			}) {
				t.Errorf("no %s record of the release:\n%s", tc.phase, stdout)
			}
			held := len(resourceHolders(ctx, env, wd, project)) == 1
			if held != tc.wantHeld {
				t.Errorf("holder kept = %t, want %t", held, tc.wantHeld)
			}
		})
	}
}
