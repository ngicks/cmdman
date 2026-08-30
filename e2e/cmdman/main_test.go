package cmdman_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// hermeticEnviron is os.Environ() with every CMDMAN_* variable removed: when
// the suite itself runs under a cmdman-supervised command, ambient identity
// (e.g. CMDMAN_CMD_ID) must not reach the child.
func hermeticEnviron() []string {
	return slices.DeleteFunc(os.Environ(), func(s string) bool {
		return strings.HasPrefix(s, "CMDMAN_")
	})
}

func must(t *testing.T, err error) {
	if err != nil {
		t.Fatalf("%v", err)
	}
}

const defaultTimeout = 10 * time.Second

// cmdmanBin is the path to the built cmdman binary, set once by TestMain.
var cmdmanBin string

func TestMain(m *testing.M) {
	// Build the cmdman binary into a temp directory.
	tmp, err := os.MkdirTemp("", "cmdman-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, "cmdman")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cmdman")
	// Build from the repository root.
	build.Dir = repoRoot()
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build cmdman: %v\n", err)
		os.Exit(1)
	}
	cmdmanBin = bin

	os.Exit(m.Run())
}

func repoRoot() string {
	// Walk up from the test file location to find go.mod.
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find repo root")
		}
		dir = parent
	}
}

type testEnv struct {
	t          *testing.T
	dataHome   string
	runtimeDir string
	confPath   string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	base, err := os.MkdirTemp("", "cmdman-e2e-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	env := &testEnv{
		t:          t,
		dataHome:   filepath.Join(base, "data"),
		runtimeDir: filepath.Join(base, "run"),
		confPath:   filepath.Join(base, "no-such-config.json"),
	}
	must(t, os.MkdirAll(env.dataHome, 0o755))
	must(t, os.MkdirAll(env.runtimeDir, 0o755))
	return env
}

// The exec* / run* helpers below are the pre-builder spelling of testEnv.Cmd,
// kept so tests that have not moved to the builder keep working off the same
// single environment composition.

func (e *testEnv) exec(ctx context.Context, args ...string) (string, string, error) {
	return e.execFull(ctx, "", nil, args...)
}

// execWithExtraEnv is like exec but appends additional environment variables.
func (e *testEnv) execWithExtraEnv(
	ctx context.Context,
	extraEnv []string,
	args ...string,
) (string, string, error) {
	return e.execFull(ctx, "", extraEnv, args...)
}

// execInDir is like exec but runs the binary with its working directory set to
// dir, so compose file auto-discovery (cmd-compose.yaml in CWD) is exercised.
func (e *testEnv) execInDir(
	ctx context.Context,
	dir string,
	args ...string,
) (string, string, error) {
	return e.execFull(ctx, dir, nil, args...)
}

// execFull runs the binary with an optional working directory (dir, "" inherits
// the parent CWD) and extra environment variables appended.
func (e *testEnv) execFull(
	ctx context.Context,
	dir string,
	extraEnv []string,
	args ...string,
) (string, string, error) {
	e.t.Helper()
	res := e.Cmd(args...).InDir(dir).WithEnv(extraEnv...).Exec(ctx)
	return res.Stdout, res.Stderr, res.Err
}

func (e *testEnv) run(ctx context.Context, args ...string) string {
	e.t.Helper()
	return e.Cmd(args...).Run(ctx, e.t)
}

func (e *testEnv) runExpectFail(ctx context.Context, args ...string) (string, string) {
	e.t.Helper()
	res := e.Cmd(args...).ExpectFail(ctx, e.t)
	return res.Stdout, res.Stderr
}

// Create creates a command and registers its removal, so a test that only needs
// a command to exist does not carry the teardown half itself.
func (e *testEnv) Create(ctx context.Context, name string, argv ...string) string {
	e.t.Helper()
	id := e.Cmd(slices.Concat([]string{"create", "-n", name, "--"}, argv)...).Run(ctx, e.t)
	// Not ctx: cleanup runs after the test's context is already cancelled.
	e.t.Cleanup(func() { e.cleanupCommand(context.Background(), name) })
	return id
}

// Run is Create for a command that has to be running: create and start in one,
// with the same registered removal.
func (e *testEnv) Run(ctx context.Context, name string, argv ...string) string {
	e.t.Helper()
	id := e.Cmd(slices.Concat([]string{"run", "-n", name, "--"}, argv)...).Run(ctx, e.t)
	// Not ctx: cleanup runs after the test's context is already cancelled.
	e.t.Cleanup(func() { e.cleanupCommand(context.Background(), name) })
	return id
}

// waitForState polls "cmdman inspect" until the command reaches the desired state
// or the timeout is reached.
func (e *testEnv) waitForState(ctx context.Context, idOrName, state string, timeout time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stdout, _, _ := e.exec(ctx, "inspect", idOrName)
		if stdout == "" {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if result["State"] == state {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// One last attempt for the error message.
	stdout, _, _ := e.exec(ctx, "inspect", idOrName)
	e.t.Fatalf("timed out waiting for state %q; last inspect output:\n%s", state, stdout)
}

// inspectJSON runs "cmdman inspect" and returns the parsed JSON.
func (e *testEnv) inspectJSON(ctx context.Context, idOrName string) map[string]any {
	e.t.Helper()
	stdout := e.run(ctx, "inspect", idOrName)
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		e.t.Fatalf("parse inspect output: %v\nraw output:\n%s", err, stdout)
	}
	return result
}

// lsJSON runs "cmdman ls --format '{{json .}}'" and returns the parsed entries.
// Each line of output is a separate JSON object.
func (e *testEnv) lsJSON(ctx context.Context, extraArgs ...string) []map[string]any {
	e.t.Helper()
	args := append([]string{"ls", "--format", "{{json .}}", "-a"}, extraArgs...)
	stdout := e.run(ctx, args...)
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil
	}
	var result []map[string]any
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			e.t.Fatalf("parse ls line: %v\nraw line:\n%s", err, line)
		}
		result = append(result, entry)
	}
	return result
}

// cleanupCommand stops and removes a command, ignoring errors.
func (e *testEnv) cleanupCommand(ctx context.Context, idOrName string) {
	e.t.Helper()
	e.exec(ctx, "stop", idOrName)
	// Wait a moment for the monitor to exit.
	time.Sleep(200 * time.Millisecond)
	e.exec(ctx, "rm", "-f", idOrName)
}

// waitUntil polls fn until it returns true or the timeout is reached.
func waitUntil(t *testing.T, timeout time.Duration, fn func() bool, msgAndArgs ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(msgAndArgs) > 0 {
		format, _ := msgAndArgs[0].(string)
		t.Fatalf(format, msgAndArgs[1:]...)
	} else {
		t.Fatal("waitUntil timed out")
	}
}
