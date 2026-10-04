package cmdman_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The tests below drive compose lifecycle hooks through the CLI. Each mark hook
// appends "<tag> <command> <event> <scale index>" to a marker file, so a test
// reads back which hooks ran for which replica, and in which order.

var markHookEvents = []string{
	"create_pre", "create_post",
	"start_pre", "start_post",
	"stop_pre", "stop_post",
	"remove_pre", "remove_post",
}

// markHookItem returns a hooks: list item named mark that sets every lifecycle
// event, indented for a command declared at two spaces.
func markHookItem(tag, marker string) string {
	script := fmt.Sprintf(
		`echo "%s $$CMDMAN_COMPOSE_COMMAND $$CMDMAN_COMPOSE_HOOK_EVENT `+
			`$$CMDMAN_COMPOSE_SCALE_INDEX" >> %s`,
		tag, shellQuote(marker),
	)
	var b strings.Builder
	b.WriteString("      - name: mark\n")
	for _, ev := range markHookEvents {
		fmt.Fprintf(&b, "        %s: [sh, -c, %q]\n", ev, script)
	}
	return b.String()
}

// markedWebYAML declares one long-running command web with scale replicas and
// a mark hook tagged tag.
func markedWebYAML(project, marker, tag string, scale int) string {
	return fmt.Sprintf(`name: %s
commands:
  web:
    scale: %d
    args: [sleep, "300"]
    hooks:
%s`, project, scale, markHookItem(tag, marker))
}

// markedEvents returns "<tag> <event>" for every line of marker written for
// replica idx of command, in order.
func markedEvents(t *testing.T, marker, command string, idx int) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(readFile(t, marker), "\n") {
		f := strings.Fields(line)
		if len(f) == 4 && f[1] == command && f[3] == strconv.Itoa(idx) {
			out = append(out, f[0]+" "+f[2])
		}
	}
	return out
}

func clearMarker(t *testing.T, marker string) {
	t.Helper()
	must(t, os.WriteFile(marker, nil, 0o644))
}

// cleanupIntermediates removes the hook commands and resource holders of a
// project, which carry no project labels for cleanupProject to go by.
func cleanupIntermediates(ctx context.Context, e *testEnv, wd, project string) {
	ids, _, _ := e.exec(ctx, "ls", "-a",
		"-l", "cmdman.compose.hooks.workdir="+wd,
		"-l", "cmdman.compose.hooks.project="+project,
		"--format", "{{.ID}}",
	)
	for id := range strings.FieldsSeq(ids) {
		e.exec(ctx, "rm", "-f", id)
	}
}

// replicaID waits for replica idx of command to exist and returns its ID.
func replicaID(ctx context.Context, t *testing.T, e *testEnv, wd, project, command string,
	idx int,
) string {
	t.Helper()
	entry := composeReplica(ctx, e, wd, project, command, idx)
	if entry == nil {
		t.Fatalf("replica %d of %s does not exist", idx, command)
	}
	return entry["ID"].(string)
}

// hookProgressEvent is the part of a --progress json record that names a hook
// run.
type hookProgressEvent struct {
	Command    string `json:"command"`
	Phase      string `json:"phase"`
	ScaleIndex int    `json:"scaleIndex"`
	Hook       string `json:"hook"`
	Lifecycle  string `json:"lifecycle"`
	Exec       string `json:"exec"`
}

func hookProgress(t *testing.T, stdout string) []hookProgressEvent {
	t.Helper()
	var out []hookProgressEvent
	for line := range strings.SplitSeq(stdout, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		var ev hookProgressEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse progress line %q: %v", line, err)
		}
		if ev.Hook != "" {
			out = append(out, ev)
		}
	}
	return out
}

func TestComposeHooksUpRunsCreateAndStartHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-up"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 2))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})

	stdout := env.Cmd("compose", "--workdir", wd, "-f", composePath,
		"up", "--progress", "json").Run(ctx, t)

	for idx := 1; idx <= 2; idx++ {
		got := markedEvents(t, marker, "web", idx)
		want := []string{"v1 create_pre", "v1 create_post", "v1 start_pre", "v1 start_post"}
		if !slices.Equal(got, want) {
			t.Errorf("hooks of web-%d = %q, want %q", idx, got, want)
		}
	}

	t.Run("progress json carries hook records", func(t *testing.T) {
		events := hookProgress(t, stdout)
		for _, want := range []hookProgressEvent{
			{Command: "web-1", Phase: "hook-running", ScaleIndex: 1, Lifecycle: "create_pre"},
			{Command: "web-2", Phase: "hook-succeeded", ScaleIndex: 2, Lifecycle: "start_post"},
		} {
			if !slices.ContainsFunc(events, func(ev hookProgressEvent) bool {
				return ev.Command == want.Command && ev.Phase == want.Phase &&
					ev.ScaleIndex == want.ScaleIndex && ev.Hook == "mark" &&
					ev.Lifecycle == want.Lifecycle &&
					strings.HasSuffix(ev.Exec, ".hook.mark."+want.Lifecycle)
			}) {
				t.Errorf("no %+v among the hook records:\n%s", want, stdout)
			}
		}
	})

	t.Run("no hook command is left", func(t *testing.T) {
		names := env.Cmd("ls", "--all", "--format", "{{.Name}}").Run(ctx, t)
		if strings.Contains(names, ".hook.") {
			t.Fatalf("a hook command is left after success:\n%s", names)
		}
	})
}

func TestComposeHooksResourcePerReplica(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-resource"
	valueFile := func(idx int) string {
		return filepath.Join(wd, "value-"+strconv.Itoa(idx))
	}
	script := fmt.Sprintf(
		`echo "$$(cmdman compose resource get web slot)" > %s-$$CMDMAN_COMPOSE_SCALE_INDEX; `+
			`exec sleep 300`,
		shellQuote(filepath.Join(wd, "value")),
	)
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    scale: 2
    args: [sh, -c, %q]
    hooks:
      - name: slot
        resource: slot
        create_pre: [sh, -c, "echo slot-$$CMDMAN_COMPOSE_SCALE_INDEX"]
`, project, script))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	// The replicas inherit PATH from the environment compose runs in, which is
	// how their `cmdman` resolves to the binary under test.
	compose("up").
		WithEnv("PATH="+filepath.Dir(cmdmanBin)+string(os.PathListSeparator)+
			os.Getenv("PATH")).
		Run(ctx, t)

	for idx := 1; idx <= 2; idx++ {
		want := "slot-" + strconv.Itoa(idx)
		got := compose("resource", "get", "--scale", strconv.Itoa(idx), "web", "slot").Run(ctx, t)
		if got != want {
			t.Errorf("resource get --scale %d = %q, want %q", idx, got, want)
		}
		waitUntil(t, defaultTimeout, func() bool {
			return readFile(t, valueFile(idx)) == want+"\n"
		}, "replica %d never read its own value %q", idx, want)
	}
}

func TestComposeHooksStartPreFailure(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-start-fail"
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  db:
    args: [sleep, "300"]
    hooks:
      - name: gate
        start_pre: [sh, -c, "echo gate-closed; exit 3"]
  app:
    args: [sleep, "300"]
    after:
      db:
        condition: running
`, project))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})

	res := env.Cmd("compose", "--workdir", wd, "-f", composePath,
		"up", "--progress", "json").ExpectFail(ctx, t)

	for _, command := range []string{"db", "app"} {
		if st := composeReplicaState(ctx, env, wd, project, command, 1); st != "created" {
			t.Errorf("%s should stay created, got %q", command, st)
		}
	}
	if !progressReached(parseProgress(t, res.Stdout), "app", "error") {
		t.Errorf("app should be reported blocked:\n%s", res.Stdout)
	}

	db := composeReplica(ctx, env, wd, project, "db", 1)["Name"].(string)
	logs := env.Cmd("logs", db+".hook.gate.start_pre").Run(ctx, t)
	if !strings.Contains(logs, "gate-closed") {
		t.Fatalf("the failed hook command should keep its output, got:\n%s", logs)
	}
}

func TestComposeHooksEditRunsOldThenNewHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-edit"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 1))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	up := func() {
		env.Cmd("compose", "--workdir", wd, "-f", composePath, "up").Run(ctx, t)
	}

	up()
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "web", 1), "running",
		defaultTimeout)
	clearMarker(t, marker)

	writeComposeFile(t, wd, markedWebYAML(project, marker, "v2", 1))
	up()

	got := markedEvents(t, marker, "web", 1)
	want := []string{
		"v1 stop_pre", "v1 stop_post", "v1 remove_pre", "v1 remove_post",
		"v2 create_pre", "v2 create_post", "v2 start_pre", "v2 start_post",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("hooks across the edit = %q, want %q", got, want)
	}
}

func TestComposeHooksScaleDownRunsStopAndRemoveHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-scale"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 2))
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

	compose("scale", "web=1").Run(ctx, t)

	want := []string{"v1 stop_pre", "v1 stop_post", "v1 remove_pre", "v1 remove_post"}
	if got := markedEvents(t, marker, "web", 2); !slices.Equal(got, want) {
		t.Errorf("hooks of the removed replica = %q, want %q", got, want)
	}
	if got := markedEvents(t, marker, "web", 1); len(got) != 0 {
		t.Errorf("the kept replica should run no hooks, ran %q", got)
	}
	if composeReplica(ctx, env, wd, project, "web", 2) != nil {
		t.Errorf("replica 2 should be removed")
	}
}

func TestComposeHooksRemoveOrphan(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-orphan"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  keep:
    args: [sleep, "300"]
  stopped:
    args: ["true"]
    hooks:
%s  running:
    args: [sleep, "300"]
    hooks:
%s`, project, markHookItem("v1", marker), markHookItem("v1", marker)))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})

	env.Cmd("compose", "--workdir", wd, "-f", composePath, "up").Run(ctx, t)
	env.waitForState(ctx, replicaID(ctx, t, env, wd, project, "stopped", 1), "exited",
		defaultTimeout)
	runningID := replicaID(ctx, t, env, wd, project, "running", 1)
	env.waitForState(ctx, runningID, "running", defaultTimeout)
	clearMarker(t, marker)

	writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  keep:
    args: [sleep, "300"]
`, project))
	// The running orphan is skipped and reported, which fails the create.
	env.Cmd("compose", "--workdir", wd, "-f", composePath, "create", "--remove-orphan").
		ExpectFail(ctx, t)

	want := []string{"v1 remove_pre", "v1 remove_post"}
	if got := markedEvents(t, marker, "stopped", 1); !slices.Equal(got, want) {
		t.Errorf("hooks of the stopped orphan = %q, want %q", got, want)
	}
	if composeReplica(ctx, env, wd, project, "stopped", 1) != nil {
		t.Errorf("the stopped orphan should be removed")
	}
	if got := markedEvents(t, marker, "running", 1); len(got) != 0 {
		t.Errorf("the running orphan should run no hooks, ran %q", got)
	}
	if st := env.inspectJSON(ctx, runningID)["State"]; st != "running" {
		t.Errorf("the running orphan should keep running, got %v", st)
	}
}

func TestComposeHooksFilelessStartRunsStoredHooks(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-fileless"
	marker := filepath.Join(wd, "marker.txt")
	composePath := writeComposeFile(t, wd, markedWebYAML(project, marker, "v1", 2))
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})

	env.Cmd("compose", "--workdir", wd, "-f", composePath, "up").Run(ctx, t)
	env.Cmd("compose", "--workdir", wd, "-f", composePath, "stop").Run(ctx, t)
	for idx := 1; idx <= 2; idx++ {
		if st := composeReplicaState(ctx, env, wd, project, "web", idx); !isStopped(st) {
			t.Fatalf("replica %d should be stopped, got %q", idx, st)
		}
	}
	clearMarker(t, marker)
	// Without the file only the labels stored on each replica know its hooks.
	must(t, os.Remove(composePath))

	env.Cmd("compose", "--workdir", wd, "--project-name", project, "start").
		InDir(t.TempDir()).Run(ctx, t)

	for idx := 1; idx <= 2; idx++ {
		want := []string{"v1 start_pre", "v1 start_post"}
		if got := markedEvents(t, marker, "web", idx); !slices.Equal(got, want) {
			t.Errorf("hooks of web-%d = %q, want %q", idx, got, want)
		}
	}
}

func TestComposeHooksCreateFailureKeepsHolder(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-create-fail"
	plainYAML := fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
`, project)
	composePath := writeComposeFile(t, wd, plainYAML)
	t.Cleanup(func() {
		ctx := context.Background()
		cleanupProject(ctx, env, wd, project)
		cleanupIntermediates(ctx, env, wd, project)
	})
	compose := func(args ...string) *Cmd {
		return env.Cmd(append([]string{"compose", "--workdir", wd, "-f", composePath}, args...)...)
	}

	// A command outside the project holds the replica's name, so the replica
	// create fails once create_pre has run.
	compose("create").Run(ctx, t)
	name := composeReplica(ctx, env, wd, project, "web", 1)["Name"].(string)
	env.Cmd("rm", "-f", name).Run(ctx, t)
	env.Create(ctx, name, "true")

	writeComposeFile(t, wd, plainYAML+`    hooks:
      - name: slot
        resource: slot
        create_pre: [echo, slot-value]
`)
	compose("create").ExpectFail(ctx, t)

	if got := compose("resource", "get", "web", "slot").Run(ctx, t); got != "slot-value" {
		t.Fatalf("resource get = %q, want the value create_pre acquired", got)
	}
}
