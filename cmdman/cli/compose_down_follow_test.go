package cli

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// progressJSON is one line of `compose down --progress json` output.
func progressJSON(t *testing.T, line progressLine) string {
	t.Helper()
	line.Op = "down"
	line.Terminal = compose.Phase(line.Phase).Terminal()
	b, err := json.Marshal(line)
	assert.NilError(t, err)
	return string(b)
}

func stdoutLine(text string) logdriver.LogLine {
	return logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte(text + "\n")}
}

func stderrLine(text string) logdriver.LogLine {
	return logdriver.LogLine{Stream: logdriver.StreamStderr, Line: []byte(text + "\n")}
}

// The summary counts the stops that went through, the removals and the
// resources left unreleased, out of the progress records alone; the last error
// line on stderr is kept for the failure it explains.
func TestDownProgressReadsTheOutput(t *testing.T) {
	removed := progressJSON(t, progressLine{Command: "db", Phase: string(compose.PhaseRemoved)})
	half := len(removed) / 2
	lines := []struct {
		line    logdriver.LogLine
		changed bool
	}{
		{stdoutLine(progressJSON(t, progressLine{Command: "web", Phase: "stopping"})), false},
		{stdoutLine(progressJSON(t, progressLine{Command: "web", Phase: "stopped"})), true},
		{stdoutLine(progressJSON(t, progressLine{
			Command: "db", Phase: "stopped", ForceKilled: true,
		})), true},
		{stdoutLine(progressJSON(t, progressLine{
			Command: "api", Phase: "error", Error: "stop command api: boom",
		})), false},
		// The remove phase reports a replica it keeps as skipped, which is neither
		// a stop nor a removal.
		{stdoutLine(progressJSON(t, progressLine{Command: "seed", Phase: "skipped"})), false},
		{stdoutLine(progressJSON(t, progressLine{Command: "web", Phase: "removed"})), true},
		// A long record can arrive in pieces.
		{logdriver.LogLine{
			Stream: logdriver.StreamStdout, Partial: true, Line: []byte(removed[:half]),
		}, false},
		{stderrLine("warning: something on the side"), false},
		{stdoutLine(removed[half:]), true},
		{stdoutLine(progressJSON(t, progressLine{
			Command: "web", Phase: "hook-output", Hook: "net", Line: `{"phase":"stopped"}`,
		})), false},
		{stdoutLine(progressJSON(t, progressLine{
			Command: "db", Phase: "unreleased", Resource: "net", Error: "network in use",
		})), true},
		{stdoutLine("not a progress record"), false},
		{stderrLine("error: 1 compose down operation(s) failed"), false},
		{stderrLine(`time=... level=WARN msg="compose down: remove failed"`), false},
	}

	var p downProgress
	for i, l := range lines {
		if got := p.add(l.line); got != l.changed {
			t.Errorf("line %d (%q): changed = %v, want %v", i, l.line.Line, got, l.changed)
		}
	}
	assert.DeepEqual(t, p.summary, tui.DownSummary{
		Stopped: 2, Removed: 2, ForceKilled: 1, Unreleased: 1,
	})
	assert.Equal(t, p.lastErr, "1 compose down operation(s) failed")
}

// downJobScript is a stand-in for the job's `cmdman compose down`: it writes
// lines to stdout, then errLine to stderr when there is one, and exits with
// code.
func downJobScript(lines []string, errLine string, code int) string {
	var b strings.Builder
	b.WriteString("printf '%s\\n'")
	for _, l := range lines {
		b.WriteString(" '" + l + "'")
	}
	if errLine != "" {
		b.WriteString("; echo '" + errLine + "' >&2")
	}
	b.WriteString("; exit " + strconv.Itoa(code))
	return b.String()
}

// collectDown drains a followed job's stream and returns the summaries it
// reported on the way and its result.
func collectDown(t *testing.T, s tui.DownStream) ([]tui.DownSummary, tui.DownSummary, error) {
	t.Helper()
	var got []tui.DownSummary
	timeout := time.After(20 * time.Second)
	for {
		select {
		case summary, ok := <-s.Summaries():
			if !ok {
				final, err := s.Result()
				assert.NilError(t, s.Close())
				return got, final, err
			}
			got = append(got, summary)
		case <-timeout:
			t.Fatal("the followed job never ended")
		}
	}
}

// A followed job reports what its output says as it runs and, once it is over,
// the summary of its whole output with the failure its exit status stands for.
func TestFollowComposeDownJob(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })
	start, _ := startDownJobInProcess(t, cfg, svc)

	stopped := progressJSON(t, progressLine{Command: "web", Phase: "stopped"})
	removed := progressJSON(t, progressLine{Command: "web", Phase: "removed"})

	// run starts a job running script and follows it the way a widget does: it
	// was running when it was launched.
	run := func(t *testing.T, script string) (string, []tui.DownSummary, tui.DownSummary, error) {
		t.Helper()
		req := downJobTestRequest(t, svc, t.TempDir(), "tools")
		req.Argv = []string{"/bin/sh", "-c", script}
		res, err := svc.Create(t.Context(), req)
		assert.NilError(t, err)
		assert.NilError(t, start(t.Context(), res.ID))
		s, err := followComposeDownJobWithin(
			t.Context(), svc, tui.DownJob{ID: res.ID},
			50*time.Millisecond, time.Second, time.Second,
		)
		assert.NilError(t, err)
		progress, final, err := collectDown(t, s)
		return res.ID, progress, final, err
	}

	t.Run("a down that went through", func(t *testing.T) {
		id, progress, final, err := run(t, downJobScript([]string{stopped, removed}, "", 0))
		assert.NilError(t, err)
		assert.DeepEqual(t, final, tui.DownSummary{Stopped: 1, Removed: 1})
		for _, p := range progress {
			assert.Assert(t, p.Running, "a summary on the way is a running one: %+v", p)
		}

		// Found again once it is over, the job reports its end alone.
		s, err := followComposeDownJobWithin(
			t.Context(), svc, tui.DownJob{ID: id, Finished: true},
			50*time.Millisecond, time.Second, time.Second,
		)
		assert.NilError(t, err)
		progress, again, err := collectDown(t, s)
		assert.NilError(t, err)
		assert.Equal(t, len(progress), 0)
		assert.DeepEqual(t, again, final)
	})

	t.Run("a down that failed", func(t *testing.T) {
		_, _, final, err := run(t, downJobScript(
			[]string{stopped}, "error: 1 compose down operation(s) failed", 1,
		))
		assert.DeepEqual(t, final, tui.DownSummary{Stopped: 1})
		assert.ErrorContains(t, err, "exit status 1: 1 compose down operation(s) failed")
	})

	t.Run("a down that failed saying nothing", func(t *testing.T) {
		_, _, _, err := run(t, "exit 3")
		assert.Error(t, err, "exit status 3")
	})

	t.Run("a down followed while it runs", func(t *testing.T) {
		script := "printf '%s\\n' '" + stopped + "'; sleep 1; printf '%s\\n' '" + removed + "'"
		_, progress, final, err := run(t, script)
		assert.NilError(t, err)
		// The job sits a second on its first record, which is long enough for that
		// report to be read before a later one replaces it.
		assert.Assert(t, slices.Contains(progress, tui.DownSummary{Stopped: 1, Running: true}),
			"the stop should be reported while the job runs: %+v", progress)
		assert.DeepEqual(t, final, tui.DownSummary{Stopped: 1, Removed: 1})
	})
}

// A job whose run ended without an exit status stands for a failed down.
func TestFollowComposeDownJobThatFailed(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })

	res, err := svc.Create(t.Context(), downJobTestRequest(t, svc, t.TempDir(), "tools"))
	assert.NilError(t, err)
	setJobState(t, cfg, res.ID, model.EventTypeFailed, &model.CommandState{
		Error: "monitor died unexpectedly",
	})

	s, err := followComposeDownJobWithin(
		t.Context(), svc, tui.DownJob{ID: res.ID, Finished: true},
		50*time.Millisecond, time.Second, time.Second,
	)
	assert.NilError(t, err)
	_, _, err = collectDown(t, s)
	assert.Error(t, err, "compose down job failed: monitor died unexpectedly")
}

// A job whose record is gone, with no exit on record, is not waited on for
// good.
func TestFollowComposeDownJobThatIsGone(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })

	s, err := followComposeDownJobWithin(
		t.Context(), svc, tui.DownJob{ID: "0123456789abcdef0123456789abcdef"},
		50*time.Millisecond, 200*time.Millisecond, time.Second,
	)
	assert.NilError(t, err)
	_, _, err = collectDown(t, s)
	assert.ErrorContains(t, err, "is gone without reporting an exit")
}

// A record left created that nothing is bringing up never ran: found, it is
// over, and following it ends at once with no report of a teardown running.
// The follow does not wait on such a record even when it is handed one as
// pending.
func TestFollowComposeDownJobThatNeverRan(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })
	dir := t.TempDir()
	res, err := svc.Create(t.Context(), downJobTestRequest(t, svc, dir, "tools"))
	assert.NilError(t, err)

	found, ok, err := findComposeDownJob(
		t.Context(), svc, ComposeDownJobOptions{WorkDir: dir, Project: "tools"},
	)
	assert.NilError(t, err)
	assert.Assert(t, ok)
	job := downJobOf(found)
	assert.DeepEqual(t, job, tui.DownJob{ID: res.ID, Finished: true})

	for _, job := range []tui.DownJob{job, {ID: res.ID}} {
		begun := time.Now()
		s, err := followComposeDownJobWithin(
			t.Context(), svc, job, 50*time.Millisecond, time.Second, time.Minute,
		)
		assert.NilError(t, err)
		progress, final, err := collectDown(t, s)
		assert.Assert(t, errors.Is(err, errComposeDownJobNotStarted), "got %v", err)
		assert.DeepEqual(t, final, tui.DownSummary{})
		assert.Assert(t, time.Since(begun) < 5*time.Second,
			"the follow waited %s on a job that never ran", time.Since(begun))
		if job.Finished {
			assert.Equal(t, len(progress), 0, "a job that never ran was reported running")
		}
	}
}

// A record left created while a launch is still on its way is followed as one
// under way, and over once the launch lets go without having got it going.
func TestFollowComposeDownJobThatALaunchLetsGo(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })

	// follow follows a created record while hold stands for the launch, and
	// returns once hold's release has ended the follow.
	follow := func(t *testing.T, hold func(id, name string) (release func())) {
		t.Helper()
		req := downJobTestRequest(t, svc, t.TempDir(), "tools")
		res, err := svc.Create(t.Context(), req)
		assert.NilError(t, err)
		release := hold(res.ID, req.Name)

		s, err := followComposeDownJobWithin(
			t.Context(), svc, tui.DownJob{ID: res.ID},
			50*time.Millisecond, time.Second, time.Minute,
		)
		assert.NilError(t, err)
		first, ok := <-s.Summaries()
		assert.Assert(t, ok && first.Running, "a launch in flight is a teardown under way")
		select {
		case summary, ok := <-s.Summaries():
			t.Fatalf("the follow moved on while the launch was in flight: %+v, %v", summary, ok)
		case <-time.After(300 * time.Millisecond):
		}

		release()
		_, final, err := collectDown(t, s)
		assert.Assert(t, errors.Is(err, errComposeDownJobNotStarted), "got %v", err)
		assert.DeepEqual(t, final, tui.DownSummary{})
	}

	t.Run("a monitor holding the PID lock", func(t *testing.T) {
		follow(t, func(id, _ string) func() {
			release, held, err := monitor.HoldPIDLock(cfg, id)
			assert.NilError(t, err)
			assert.Assert(t, held)
			return release
		})
	})

	t.Run("a launch holding the job's lock", func(t *testing.T) {
		follow(t, func(_, name string) func() {
			unlock, err := lockComposeJob(t.Context(), cfg, name)
			assert.NilError(t, err)
			return unlock
		})
	})
}

// A launch that never lets go is not waited on past the start wait.
func TestFollowComposeDownJobThatNeverGetsGoing(t *testing.T) {
	cfg := frameSvcConfig(t)
	svc := cmdman.NewService(cfg)
	t.Cleanup(func() { _ = svc.Close() })
	res, err := svc.Create(t.Context(), downJobTestRequest(t, svc, t.TempDir(), "tools"))
	assert.NilError(t, err)
	holdMonitorLock(t, cfg, res.ID)

	s, err := followComposeDownJobWithin(
		t.Context(), svc, tui.DownJob{ID: res.ID},
		50*time.Millisecond, time.Second, 300*time.Millisecond,
	)
	assert.NilError(t, err)
	_, _, err = collectDown(t, s)
	assert.Assert(t, errors.Is(err, errComposeDownJobNotStarted), "got %v", err)
}
