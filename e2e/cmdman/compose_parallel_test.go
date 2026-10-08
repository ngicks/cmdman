package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// composeParallelReplicas is the scale of the command composeParallelYAML
// declares.
const composeParallelReplicas = 6

// composeParallelYAML returns a project of one command scaled to
// composeParallelReplicas. Each replica appends a line to readyPath once its
// TERM trap is set. On SIGTERM it appends "enter <ts>" to stopLog, takes a
// second, appends "leave <ts>" and exits.
func composeParallelYAML(project, readyPath, stopLog string) string {
	// The script goes through compose interpolation, so a $ the shell is to see
	// is written $$.
	script := fmt.Sprintf(`trap 'echo "enter $$(date +%%s.%%N)" >> "%[1]s"
sleep 1
echo "leave $$(date +%%s.%%N)" >> "%[1]s"
kill $$! 2>/dev/null
exit 0' TERM
sleep 300 &
echo ready >> "%[2]s"
wait`, stopLog, readyPath)
	return fmt.Sprintf(`name: %s
commands:
  slow:
    args: [sh, -c, %q]
    scale: %d
`, project, script, composeParallelReplicas)
}

// stopOverlap returns the most replicas that stopLog shows inside their TERM
// trap at once, and how many entered it. The replicas append to one file, so
// its lines stand in the order they were written.
func stopOverlap(stopLog string) (most, entered int) {
	inside := 0
	for line := range strings.Lines(stopLog) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "enter":
			entered++
			inside++
			most = max(most, inside)
		case "leave":
			inside--
		}
	}
	return most, entered
}

// upParallelProject brings up a composeParallelYAML project and waits until
// every replica has set its TERM trap. It returns the arguments that select the
// project and the path of the stop log.
func upParallelProject(
	ctx context.Context,
	t *testing.T,
	env *testEnv,
	project string,
) (selection []string, stopLog string) {
	t.Helper()
	wd := composeWorkdir(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	stopLog = filepath.Join(dir, "stop.log")
	composePath := writeComposeFile(t, wd, composeParallelYAML(project, ready, stopLog))
	t.Cleanup(func() { cleanupProject(context.Background(), env, wd, project) })
	selection = []string{"compose", "--workdir", wd, "-f", composePath}
	env.Cmd(append(selection, "up")...).Run(ctx, t)
	waitUntil(t, defaultTimeout, func() bool {
		return strings.Count(readFile(t, ready), "ready") == composeParallelReplicas
	}, "not every replica set its TERM trap")
	return selection, stopLog
}

// TestComposeDown_ParallelLimit pins how many replicas one compose down stops
// at once: --parallel, then CMDMAN_COMPOSE_PARALLEL_LIMIT, then 4.
func TestComposeDown_ParallelLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		env     []string
		maxMost int
		minMost int
	}{
		{name: "default", maxMost: 4},
		{name: "flag", args: []string{"--parallel", "2"}, maxMost: 2},
		{name: "env", env: []string{"CMDMAN_COMPOSE_PARALLEL_LIMIT=2"}, maxMost: 2},
		{
			name:    "flag over env",
			args:    []string{"--parallel", "2"},
			env:     []string{"CMDMAN_COMPOSE_PARALLEL_LIMIT=-1"},
			maxMost: 2,
		},
		// Every replica stops at once. One of them may still enter its trap after
		// the first left it on a loaded machine.
		{
			name:    "unlimited",
			args:    []string{"--parallel", "-1"},
			maxMost: composeParallelReplicas,
			minMost: composeParallelReplicas - 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := testContext(t)
			env := newTestEnv(t)
			project := "tc-parallel-" + strings.ReplaceAll(tc.name, " ", "-")
			selection, stopLog := upParallelProject(ctx, t, env, project)

			args := slices.Concat(selection, []string{"down"}, tc.args)
			env.Cmd(args...).WithEnv(tc.env...).Run(ctx, t)

			log := readFile(t, stopLog)
			most, entered := stopOverlap(log)
			t.Logf("down stopped at most %d replicas at once", most)
			if entered != composeParallelReplicas {
				t.Fatalf("%d replicas trapped TERM, want %d:\n%s",
					entered, composeParallelReplicas, log)
			}
			if most > tc.maxMost || most < tc.minMost {
				t.Errorf("down stopped %d replicas at once, want %d to %d:\n%s",
					most, tc.minMost, tc.maxMost, log)
			}
		})
	}
}

// TestComposeDown_ParallelRejected pins a --parallel or
// CMDMAN_COMPOSE_PARALLEL_LIMIT that is neither positive nor -1 failing the
// down before it stops any replica.
func TestComposeDown_ParallelRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		args       []string
		env        []string
		wantStderr string
	}{
		{name: "flag zero", args: []string{"--parallel", "0"}, wantStderr: "--parallel"},
		{name: "flag below -1", args: []string{"--parallel", "-2"}, wantStderr: "--parallel"},
		{
			name:       "env zero",
			env:        []string{"CMDMAN_COMPOSE_PARALLEL_LIMIT=0"},
			wantStderr: "CMDMAN_COMPOSE_PARALLEL_LIMIT",
		},
	}
	ctx := testContext(t)
	env := newTestEnv(t)
	selection, stopLog := upParallelProject(ctx, t, env, "tc-parallel-rejected")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := slices.Concat(selection, []string{"down"}, tc.args)
			env.Cmd(args...).WithEnv(tc.env...).ExpectFail(ctx, t, tc.wantStderr)
		})
	}

	if log := readFile(t, stopLog); log != "" {
		t.Errorf("a rejected down stopped replicas:\n%s", log)
	}
	entries := env.lsJSON(ctx, "-l", "cmdman.compose.project=tc-parallel-rejected")
	if len(entries) != composeParallelReplicas {
		t.Fatalf("a rejected down left %d replicas, want %d",
			len(entries), composeParallelReplicas)
	}
	for _, e := range entries {
		if e["State"] != "running" {
			t.Errorf("replica %v is %v after a rejected down, want running", e["Name"], e["State"])
		}
	}
}
