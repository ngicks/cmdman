package cmdman_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

// TestComposeHooksExitCodeSurvivesConcurrentList runs compose up and down while
// lists run in a tight loop against the same store. A list marks a command
// failed, without an exit code, when no monitor holds the command's lock. A
// hook command's monitor records the exit code first and only then lets go of
// the lock, so a list that read the hook command as running before the exit and
// probed the lock after it would overwrite the recorded exit, and a hook that
// exited 0 would read as one that ended without an exit code. A hook that
// ended before its start returned reads as a failed start instead.
func TestComposeHooksExitCodeSurvivesConcurrentList(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-hooks-exit-code-list"
	released := filepath.Join(wd, "released.txt")
	release := `echo "$$CMDMAN_COMPOSE_RESOURCE_VALUE" >> ` + shellQuote(released)
	composePath := writeComposeFile(t, wd, fmt.Sprintf(`name: %s
commands:
  web:
    args: [sleep, "300"]
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

	// The first open of a store creates its schema, and two processes that both
	// find the schema missing race to create it. One list ahead of the loop
	// creates it, so the loop and compose only ever open a store that has one.
	env.Cmd("ls", "-a").Run(ctx, t)

	// stopCtx only ends the loop between lists: cancelling the context a list
	// runs on would kill it halfway through and report that as its failure.
	stopCtx, stop := context.WithCancel(ctx)
	var g errgroup.Group
	var lists int
	g.Go(func() error {
		for stopCtx.Err() == nil {
			res := env.Cmd("ls", "-a").Exec(ctx)
			if res.Err != nil && ctx.Err() == nil {
				return fmt.Errorf("ls -a: %w\nstderr:\n%s", res.Err, res.Stderr)
			}
			lists++
		}
		return nil
	})
	defer func() {
		stop()
		_ = g.Wait()
	}()

	const iterations = 10
	for i := range iterations {
		for _, op := range []string{"up", "down"} {
			res := compose(op).Exec(ctx)
			if res.Err != nil {
				t.Fatalf("iteration %d: compose %s failed: %v\nstdout:\n%s\nstderr:\n%s",
					i, op, res.Err, res.Stdout, res.Stderr)
			}
			if out := res.Stdout + "\n" + res.Stderr; strings.Contains(
				out, "ended without an exit code") {
				t.Fatalf("iteration %d: compose %s reported a hook without an exit code:\n%s",
					i, op, out)
			}
		}
	}

	stop()
	if err := g.Wait(); err != nil {
		t.Errorf("the concurrent list failed: %v", err)
	}
	if lists == 0 {
		t.Errorf("no list ran alongside compose up and down")
	}
	if got := fileLines(t, released); len(got) != iterations {
		t.Errorf("released %d times, want %d: %q", len(got), iterations, got)
	}
}
