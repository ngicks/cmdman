package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/go-common/contextkey"
	"github.com/ngicks/go-common/iopipe"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// fakeAttachSvc stands in for *cmdman.Service on the by-command attach path.
// OpenAttachSession is never reached by these tests: they describe a command
// that is not running, so the sticky loop goes straight to the wait prompt.
type fakeAttachSvc struct {
	out      *cmdman.InspectOutput
	err      error
	inspects int
}

// exitedSvcAt describes an exited command configured to run in dir.
func exitedSvcAt(dir string) *fakeAttachSvc {
	return &fakeAttachSvc{
		out: &cmdman.InspectOutput{
			State:  model.EventTypeExited,
			Config: &model.CommandConfig{Dir: dir},
		},
	}
}

func (f *fakeAttachSvc) Inspect(context.Context, string) (*cmdman.InspectOutput, error) {
	f.inspects++
	return f.out, f.err
}

func (f *fakeAttachSvc) OpenAttachSession(context.Context, string) (*cmdman.Session, error) {
	return nil, errors.New("fakeAttachSvc: no attach stream")
}

func (f *fakeAttachSvc) Restart(
	context.Context,
	cmdman.RestartRequest,
) ([]cmdman.RestartResult, error) {
	return nil, nil
}

// TestCompleteWorkDir covers the defaulting itself: which of the caller's value
// and the command's configured directory ends up in the option, and what an
// unreadable command config does to it.
func TestCompleteWorkDir(t *testing.T) {
	t.Run("empty WorkDir is filled from the command config", func(t *testing.T) {
		svc := exitedSvcAt("/cmd/dir")

		opts := completeWorkDir(t.Context(), svc, "cmd", AttachOptions{})

		assert.Equal(t, opts.WorkDir, "/cmd/dir")
	})

	t.Run("an explicit WorkDir wins and skips the lookup", func(t *testing.T) {
		svc := exitedSvcAt("/cmd/dir")

		opts := completeWorkDir(t.Context(), svc, "cmd", AttachOptions{WorkDir: "/caller/dir"})

		assert.Equal(t, opts.WorkDir, "/caller/dir")
		assert.Equal(t, svc.inspects, 0)
	})

	t.Run("an unreadable command config leaves it empty", func(t *testing.T) {
		svc := &fakeAttachSvc{err: errors.New("no such command")}
		var logs bytes.Buffer
		ctx := contextkey.WithSlogLogger(
			t.Context(),
			slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		)

		opts := completeWorkDir(ctx, svc, "cmd", AttachOptions{})

		assert.Equal(t, opts.WorkDir, "")
		// The lookup is best-effort, so the reason it failed is only ever
		// readable in the log.
		assert.Assert(t, strings.Contains(logs.String(), "no such command"),
			"the failed lookup went unlogged; got %q", logs.String())
	})

	t.Run("a command with no configured dir leaves it empty", func(t *testing.T) {
		svc := &fakeAttachSvc{out: &cmdman.InspectOutput{State: model.EventTypeExited}}

		opts := completeWorkDir(t.Context(), svc, "cmd", AttachOptions{})

		assert.Equal(t, opts.WorkDir, "")
	})
}

// TestAttachCommand_WorkDir covers the one-shot entry point (`attach
// --auto-exit`) doing the same defaulting as the sticky one. The fake has no
// attach stream, so the run ends at the session open — after the lookup that is
// under test here.
func TestAttachCommand_WorkDir(t *testing.T) {
	t.Run("resolves the command's configured dir", func(t *testing.T) {
		svc := exitedSvcAt(t.TempDir())

		err := AttachCommand(t.Context(), svc, "cmd", AttachOptions{})

		assert.ErrorContains(t, err, "no attach stream")
		assert.Equal(t, svc.inspects, 1)
	})

	t.Run("an explicit WorkDir skips the lookup", func(t *testing.T) {
		svc := exitedSvcAt(t.TempDir())

		err := AttachCommand(t.Context(), svc, "cmd", AttachOptions{WorkDir: t.TempDir()})

		assert.ErrorContains(t, err, "no attach stream")
		assert.Equal(t, svc.inspects, 0)
	})
}

// TestAttachCommandSticky_WorkDir covers the defaulting where it takes effect:
// a caller that only hands over the service and the command gets the chdir, and
// gets it before the loop starts — the command here never runs, so a chdir tied
// to an open attach session would never happen at all.
//
// Every case moves the process-wide cwd, so none of them may run in parallel.
// t.Chdir both establishes a known starting point and restores the original
// cwd when the subtest ends.
func TestAttachCommandSticky_WorkDir(t *testing.T) {
	t.Run("chdirs into the command's configured dir", func(t *testing.T) {
		target := t.TempDir()
		t.Chdir(t.TempDir())

		err := attachStickyUntilDetach(t, exitedSvcAt(target), "")

		assert.NilError(t, err)
		assert.Equal(t, resolvedCwd(t), resolveDir(t, target))
	})

	t.Run("an explicit WorkDir overrides the configured dir", func(t *testing.T) {
		target := t.TempDir()
		t.Chdir(t.TempDir())

		err := attachStickyUntilDetach(t, exitedSvcAt(t.TempDir()), target)

		assert.NilError(t, err)
		assert.Equal(t, resolvedCwd(t), resolveDir(t, target))
	})

	t.Run("an unreadable command config keeps the cwd and the attach", func(t *testing.T) {
		base := t.TempDir()
		t.Chdir(base)

		err := attachStickyUntilDetach(t, &fakeAttachSvc{err: errors.New("gone")}, "")

		assert.NilError(t, err)
		assert.Equal(t, resolvedCwd(t), resolveDir(t, base))
	})

	t.Run("a nonexistent dir keeps the cwd and the attach", func(t *testing.T) {
		base := t.TempDir()
		t.Chdir(base)

		err := attachStickyUntilDetach(t, exitedSvcAt(filepath.Join(base, "removed")), "")

		assert.NilError(t, err)
		assert.Equal(t, resolvedCwd(t), resolveDir(t, base))
	})
}

// attachStickyUntilDetach runs [AttachCommandSticky] against a command the fake
// reports as not running, with an already-exhausted stdin source: the loop goes
// straight to the wait prompt, finds nobody to answer it, and detaches. What is
// left behind is the chdir it performed on the way in.
//
// Stdin/Stdout are pipes rather than TTYs, so raw mode is skipped and nothing is
// written to them.
func attachStickyUntilDetach(t *testing.T, svc attachSvc, workDir string) error {
	t.Helper()

	stdinR, stdinW, err := os.Pipe()
	assert.NilError(t, err)
	stdoutR, stdoutW, err := os.Pipe()
	assert.NilError(t, err)
	t.Cleanup(func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdinPipe := iopipe.NewReader(bytes.NewReader(nil))
	stdoutPipe := iopipe.NewWriter(io.Discard)
	var wg sync.WaitGroup
	// Cleanup, not defer: both Run calls return only once ctx is done, and the
	// deferred cancel above runs before any cleanup.
	t.Cleanup(wg.Wait)
	wg.Go(func() { stdinPipe.Run(ctx) })
	wg.Go(func() { stdoutPipe.Run(ctx) })

	return AttachCommandSticky(ctx, svc, "cmd", AttachOptions{
		DetachKeys: DefaultDetachKeys,
		WorkDir:    workDir,
		Stdin:      stdinR,
		Stdout:     stdoutW,
		StdinPipe:  stdinPipe,
		StdoutPipe: stdoutPipe,
	})
}
