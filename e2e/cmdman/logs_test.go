package cmdman_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLogs_CapturesOutput(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "log-producer", "--", "/bin/sh", "-c",
		"echo 'line-one'; echo 'line-two'; echo 'line-three'")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "log-producer", "exited", defaultTimeout)

	// Logs should be readable after exit because they come from the on-disk file.
	stdout := env.run(ctx, "logs", "log-producer")
	for _, want := range []string{"line-one", "line-two", "line-three"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected %q in logs, got:\n%s", want, stdout)
		}
	}
}

func TestLogs_RunningCommand(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Start a command that outputs something then sleeps.
	id := env.run(ctx, "run", "-n", "log-running", "--", "/bin/sh", "-c",
		"echo 'hello-from-logs'; sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "log-running", "running", defaultTimeout)

	// Give it a moment to produce output.
	time.Sleep(500 * time.Millisecond)

	// Read logs (non-follow).
	stdout := env.run(ctx, "logs", "log-running")

	if !strings.Contains(stdout, "hello-from-logs") {
		t.Errorf("expected 'hello-from-logs' in logs output, got:\n%s", stdout)
	}
}

func TestLogs_PreservesFullHistoryBeyondScrollback(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Scrollback is 256 bytes; output is ~400 bytes. The on-disk log file is
	// independent of the in-memory ring buffer, so logs must include every line.
	id := env.run(ctx, "run", "-n", "scrollback-test",
		"--scrollback-bytes", "256",
		"--", "/bin/sh", "-c",
		"for i in $(seq 1 20); do echo \"scrollback-line-$i--\"; done; sleep 300")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "scrollback-test", "running", defaultTimeout)

	time.Sleep(500 * time.Millisecond)

	stdout := env.run(ctx, "logs", "scrollback-test")
	for i := 1; i <= 20; i++ {
		want := "scrollback-line-" + strconv.Itoa(i) + "--"
		if !strings.Contains(stdout, want) {
			t.Errorf("expected %q in logs (file should retain everything), got:\n%s", want, stdout)
		}
	}
}

// k8sFileLineRE matches a single k8s-file log entry:
// "<RFC3339Nano> stdout <F|P> <content>".
var k8sFileLineRE = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}(?:Z|[+-]\d{2}:\d{2}) stdout [FP] `,
)

var k8sFileStderrLineRE = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}(?:Z|[+-]\d{2}:\d{2}) stderr [FP] `,
)

func hasMatchingLogLine(text string, re *regexp.Regexp, content string) bool {
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if re.MatchString(line) && strings.Contains(line, content) {
			return true
		}
	}
	return false
}

func TestLogDriver_K8sFileWritesLogToCommandDir(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "k8s-log-default", "--", "/bin/sh", "-c",
		"echo 'k8s-driver-line-A'; echo 'k8s-driver-line-B'")
	t.Cleanup(func() { env.cleanupCommand(ctx, "k8s-log-default") })
	env.waitForState(ctx, "k8s-log-default", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "k8s-log-default")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	// Allow the monitor a moment to flush before the file is read.
	deadline := time.Now().Add(2 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		var err error
		data, err = os.ReadFile(logPath)
		if err == nil && len(data) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(data) == 0 {
		t.Fatalf("expected log file %s to have content", logPath)
	}

	// Every non-empty line must match the k8s-file format.
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected at least one log entry, got none")
	}
	for _, line := range lines {
		if !k8sFileLineRE.MatchString(line) {
			t.Errorf("line does not match k8s-file format: %q", line)
		}
	}

	// The actual content must be present somewhere.
	if !strings.Contains(string(data), "k8s-driver-line-A") {
		t.Errorf("expected 'k8s-driver-line-A' in log file, got:\n%s", data)
	}
	if !strings.Contains(string(data), "k8s-driver-line-B") {
		t.Errorf("expected 'k8s-driver-line-B' in log file, got:\n%s", data)
	}
}

func TestRun_DefaultPipeModeSplitsStdoutAndStderrLogs(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "pipe-split", "--", "/bin/sh", "-c",
		"printf 'pipe-out\\n'; printf 'pipe-err\\n' >&2")
	t.Cleanup(func() { env.cleanupCommand(ctx, "pipe-split") })
	env.waitForState(ctx, "pipe-split", "exited", defaultTimeout)

	waitUntil(t, defaultTimeout, func() bool {
		stdout, stderr, err := env.exec(ctx, "logs", "pipe-split")
		return err == nil &&
			strings.Contains(stdout, "pipe-out") &&
			strings.Contains(stderr, "pipe-err")
	}, "pipe-split logs did not expose both streams")

	stdout, stderr, err := env.exec(ctx, "logs", "pipe-split")
	if err != nil {
		t.Fatalf("logs failed: %v", err)
	}
	if !strings.Contains(stdout, "pipe-out") {
		t.Errorf("expected stdout log output, got stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "pipe-err") {
		t.Errorf("expected stderr log output, got stdout=%q stderr=%q", stdout, stderr)
	}

	info := env.inspectJSON(ctx, "pipe-split")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "pipe-out") || !strings.Contains(text, "pipe-err") {
		t.Fatalf("expected both streams in raw log, got:\n%s", text)
	}
	if !hasMatchingLogLine(text, k8sFileLineRE, "pipe-out") {
		t.Errorf("missing stdout-tagged pipe-out entry:\n%s", text)
	}
	if !hasMatchingLogLine(text, k8sFileStderrLineRE, "pipe-err") {
		t.Errorf("missing stderr-tagged pipe-err entry:\n%s", text)
	}
}

func TestLogDriver_NoneDoesNotCreateLogFile(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "log-none",
		"--log-driver", "none",
		"--", "/bin/sh", "-c", "echo none-driver-line")
	t.Cleanup(func() { env.cleanupCommand(ctx, "log-none") })
	env.waitForState(ctx, "log-none", "exited", defaultTimeout)

	// Allow the monitor to fully unwind in case it would otherwise create the file.
	time.Sleep(300 * time.Millisecond)

	info := env.inspectJSON(ctx, "log-none")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	if _, err := os.Stat(logPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected no log file at %s, got err=%v", logPath, err)
	}

	// The configured driver should round-trip into inspect output.
	cfg, _ := info["Config"].(map[string]any)
	if cfg["log_driver"] != "none" {
		t.Errorf("expected config.log_driver=none, got %v", cfg["log_driver"])
	}
}

func TestLogDriver_RejectsUnknownDriver(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-driver", "bogus",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "log_driver") && !strings.Contains(stderr, "log driver") {
		t.Errorf("expected error about log_driver, got stderr:\n%s", stderr)
	}
}

func TestLogOpt_PathOverridesDefaultLocation(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	customLog := filepath.Join(env.dataHome, "custom-console.log")
	env.run(ctx, "run", "-n", "log-opt-path",
		"--log-opt", "path="+customLog,
		"--", "/bin/sh", "-c", "echo opt-path-line")
	t.Cleanup(func() { env.cleanupCommand(ctx, "log-opt-path") })
	env.waitForState(ctx, "log-opt-path", "exited", defaultTimeout)

	deadline := time.Now().Add(2 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		var err error
		data, err = os.ReadFile(customLog)
		if err == nil && len(data) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(data), "opt-path-line") {
		t.Errorf("expected log line at %s, got: %q", customLog, data)
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if !k8sFileLineRE.MatchString(line) {
			t.Errorf("line does not match k8s-file format: %q", line)
		}
	}

	// The default location must not be created when path is overridden.
	info := env.inspectJSON(ctx, "log-opt-path")
	hexID, _ := info["ID"].(string)
	defaultPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	if _, err := os.Stat(defaultPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(
			"default log path %s should not exist when path opt is set; err=%v",
			defaultPath,
			err,
		)
	}

	cfg, _ := info["Config"].(map[string]any)
	logOpts, _ := cfg["log_opts"].(map[string]any)
	if logOpts["path"] != customLog {
		t.Errorf("expected config.log_opts.path=%q, got %v", customLog, logOpts["path"])
	}
}

func TestLogOpt_RejectsRelativePath(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-opt", "path=relative/console.log",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "absolute") {
		t.Errorf("expected error about absolute path, got stderr:\n%s", stderr)
	}
}

func TestLogOpt_RejectsUnknownKey(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-opt", "bogus=value",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "log_opt") && !strings.Contains(stderr, "log-opt") {
		t.Errorf("expected error about log_opt, got stderr:\n%s", stderr)
	}
}

func TestLogOpt_RejectedForNoneDriver(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-driver", "none",
		"--log-opt", "path=/tmp/cmdman-none-test.log",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "log_opt") && !strings.Contains(stderr, "log-opt") {
		t.Errorf("expected error about log_opt, got stderr:\n%s", stderr)
	}
}

func TestLogOpt_MaxSizeTruncatesFile(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Each k8s-file entry is roughly 41 bytes of overhead plus the line.
	// Print 200 ~20-byte lines and cap at 512 bytes — the file must end up
	// well below the produced volume because older entries get dropped.
	env.run(ctx, "run", "-n", "log-max-size",
		"--log-opt", "max-size=512",
		"--", "/bin/sh", "-c",
		"i=1; while [ $i -le 200 ]; do echo \"max-size-line-$i\"; i=$((i+1)); done")
	t.Cleanup(func() { env.cleanupCommand(ctx, "log-max-size") })
	env.waitForState(ctx, "log-max-size", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "log-max-size")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")

	var size int64
	waitUntil(t, 2*time.Second, func() bool {
		st, err := os.Stat(logPath)
		if err != nil {
			return false
		}
		size = st.Size()
		return size > 0
	}, "expected log file %s to have content", logPath)

	// The producer wrote at least 200 * ~17 = 3.4 kB of unframed output;
	// without truncation the framed file would be well over a kilobyte.
	// With max-size=512 it should land at roughly one or two entries.
	if size > 2*512 {
		t.Errorf("expected log file size <= 1024 bytes after truncation, got %d", size)
	}

	cfg, _ := info["Config"].(map[string]any)
	logOpts, _ := cfg["log_opts"].(map[string]any)
	if logOpts["max-size"] != "512" {
		t.Errorf("expected config.log_opts.max-size=512, got %v", logOpts["max-size"])
	}
}

func TestLogOpt_RejectsInvalidMaxSize(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-opt", "max-size=not-a-size",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "max-size") {
		t.Errorf("expected error mentioning max-size, got stderr:\n%s", stderr)
	}
}

func TestLogOpt_MaxFileRotatesArchives(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	// Each entry is ~41 bytes overhead + line; cap at 256 bytes with
	// max-file=3, so the 200-line producer will rotate many times. After
	// the run, the active file plus .1 and .2 should all exist (older
	// archives get dropped) and no .3 should be created.
	env.run(ctx, "run", "-n", "log-max-file",
		"--log-opt", "max-size=256",
		"--log-opt", "max-file=3",
		"--", "/bin/sh", "-c",
		"i=1; while [ $i -le 200 ]; do echo \"max-file-line-$i\"; i=$((i+1)); done")
	t.Cleanup(func() { env.cleanupCommand(ctx, "log-max-file") })
	env.waitForState(ctx, "log-max-file", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "log-max-file")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")

	waitUntil(t, 2*time.Second, func() bool {
		for _, suffix := range []string{".1", ".2"} {
			if _, err := os.Stat(logPath + suffix); err != nil {
				return false
			}
		}
		return true
	}, "expected .1 and .2 archives at %s", logPath)

	// .1 and .2 must be present, .3 must not.
	for _, suffix := range []string{".1", ".2"} {
		if _, err := os.Stat(logPath + suffix); err != nil {
			t.Errorf("expected archive %s, got err=%v", logPath+suffix, err)
		}
	}
	if _, err := os.Stat(logPath + ".3"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected no %s archive (max-file=3), got err=%v", logPath+".3", err)
	}

	cfg, _ := info["Config"].(map[string]any)
	logOpts, _ := cfg["log_opts"].(map[string]any)
	if logOpts["max-file"] != "3" {
		t.Errorf("expected config.log_opts.max-file=3, got %v", logOpts["max-file"])
	}
}

func TestLogOpt_RejectsInvalidMaxFile(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	_, stderr := env.runExpectFail(ctx, "run",
		"--log-opt", "max-file=abc",
		"--", "/bin/sh", "-c", "true")
	if !strings.Contains(stderr, "max-file") {
		t.Errorf("expected error mentioning max-file, got stderr:\n%s", stderr)
	}
}

func TestLogs_FollowStreamsAppendedOutput(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "follow-cmd", "--", "/bin/sh", "-c",
		"echo first; sleep 1; echo second; sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, "follow-cmd") })
	env.waitForState(ctx, "follow-cmd", "running", defaultTimeout)

	followCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stdout, _, _ := env.exec(followCtx, "logs", "-f", "follow-cmd")

	if !strings.Contains(stdout, "first") {
		t.Errorf("expected 'first' in follow output, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "second") {
		t.Errorf("expected 'second' (appended after follow began) in output, got:\n%s", stdout)
	}
}

func TestLogs_TailAfterRotation(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "logs-tail-rotation",
		"--log-opt", "max-size=256",
		"--log-opt", "max-file=3",
		"--", "/bin/sh", "-c",
		"i=1; while [ $i -le 60 ]; do printf 'tail-rotation-line-%02d\\n' \"$i\"; i=$((i+1)); done")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "logs-tail-rotation", "exited", defaultTimeout)

	stdout := env.run(ctx, "logs", "--tail", "5", "logs-tail-rotation")
	got := splitNonEmptyLines(stdout)
	if len(got) != 5 {
		t.Fatalf("expected exactly 5 tail lines, got %d:\n%s", len(got), stdout)
	}
	for i := range 5 {
		want := "tail-rotation-line-" + fmt.Sprintf("%02d", 56+i)
		if got[i] != want {
			t.Fatalf("tail line %d = %q, want %q; all lines=%v", i, got[i], want, got)
		}
	}
}

func TestLogs_SinceCrossesRotation(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "logs-since-rotation",
		"--log-opt", "max-size=1024",
		"--log-opt", "max-file=3",
		"--", "/bin/sh", "-c", strings.Join([]string{
			"echo since-before",
			"sleep 0.5",
			"i=1",
			"while [ $i -le 40 ]; do",
			"  printf 'since-after-%02d\\n' \"$i\"",
			"  i=$((i+1))",
			"done",
		}, "\n"))
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })

	info := env.inspectJSON(ctx, "logs-since-rotation")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	waitForFileContent(t, logPath, "since-before", defaultTimeout)
	t0 := time.Now().UTC()

	env.waitForState(ctx, "logs-since-rotation", "exited", defaultTimeout)

	stdout, stderr, err := env.exec(
		ctx,
		"logs",
		"--since",
		t0.Format(time.RFC3339Nano),
		"logs-since-rotation",
	)
	if err != nil {
		t.Fatalf("logs --since failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if strings.Contains(stdout, "since-before") {
		t.Fatalf(
			"logs --since included pre-t0 line; t0=%s output:\n%s",
			t0.Format(time.RFC3339Nano),
			stdout,
		)
	}
	for _, want := range []string{"since-after-01", "since-after-40"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf(
				"logs --since missing %q; t0=%s output:\n%s",
				want,
				t0.Format(time.RFC3339Nano),
				stdout,
			)
		}
	}
}

func TestLogs_FollowSeesStoredThenLive(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	id := env.run(ctx, "run", "-n", "follow-stored-live", "--", "/bin/sh", "-c",
		"echo follow-A; sleep 0.5; echo follow-B; sleep 0.5; echo follow-C; sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "follow-stored-live", "running", defaultTimeout)

	info := env.inspectJSON(ctx, "follow-stored-live")
	hexID, _ := info["ID"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	waitForFileContent(
		t,
		filepath.Join(env.dataHome, "commands", hexID, "console.log"),
		"follow-A",
		defaultTimeout,
	)

	followCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	stdout, _, _ := env.exec(followCtx, "logs", "-f", "follow-stored-live")

	assertContainsInOrder(t, stdout, "follow-A", "follow-B", "follow-C")
}

func TestLogs_FollowPreservesStreamSplit(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	script := strings.Join([]string{
		"echo split-out-1",
		"sleep 0.2",
		"echo split-err-1 >&2",
		"sleep 0.2",
		"echo split-out-2",
		"sleep 0.2",
		"echo split-err-2 >&2",
		"sleep 60",
	}, "\n")
	id := env.run(ctx, "run", "-n", "follow-stream-split", "--", "/bin/sh", "-c", script)
	t.Cleanup(func() { env.cleanupCommand(ctx, id) })
	env.waitForState(ctx, "follow-stream-split", "running", defaultTimeout)

	followCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	stdout, stderr, _ := env.exec(followCtx, "logs", "-f", "follow-stream-split")

	for _, want := range []string{"split-out-1", "split-out-2"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q; stdout=%q stderr=%q", want, stdout, stderr)
		}
		if strings.Contains(stderr, want) {
			t.Errorf("stderr unexpectedly contains stdout line %q; stderr=%q", want, stderr)
		}
	}
	for _, want := range []string{"split-err-1", "split-err-2"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q; stdout=%q stderr=%q", want, stdout, stderr)
		}
		if strings.Contains(stdout, want) {
			t.Errorf("stdout unexpectedly contains stderr line %q; stdout=%q", want, stdout)
		}
	}
}

func TestLogs_NoneDriverErrors(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "logs-none", "--log-driver", "none",
		"--", "/bin/sh", "-c", "echo unreadable; sleep 60")
	t.Cleanup(func() { env.cleanupCommand(ctx, "logs-none") })
	env.waitForState(ctx, "logs-none", "running", defaultTimeout)

	_, stderr := env.runExpectFail(ctx, "logs", "logs-none")
	if !strings.Contains(stderr, "does not retain logs") {
		t.Errorf("expected 'does not retain logs' error, got stderr:\n%s", stderr)
	}
}

func TestLogs_RespectsLogOptPath(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	customLog := filepath.Join(env.dataHome, "logs-via-opt.log")
	env.run(ctx, "run", "-n", "logs-via-opt",
		"--log-opt", "path="+customLog,
		"--", "/bin/sh", "-c", "echo from-custom-path")
	t.Cleanup(func() { env.cleanupCommand(ctx, "logs-via-opt") })
	env.waitForState(ctx, "logs-via-opt", "exited", defaultTimeout)

	stdout := env.run(ctx, "logs", "logs-via-opt")
	if !strings.Contains(stdout, "from-custom-path") {
		t.Errorf("expected log content from %s, got:\n%s", customLog, stdout)
	}
}

func splitNonEmptyLines(s string) []string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func waitForFileContent(t *testing.T, path string, want string, timeout time.Duration) {
	t.Helper()
	waitUntil(t, timeout, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(data), want)
	}, "expected %q in %s", want, path)
}

func assertContainsInOrder(t *testing.T, text string, wants ...string) {
	t.Helper()
	pos := 0
	for _, want := range wants {
		idx := strings.Index(text[pos:], want)
		if idx < 0 {
			t.Fatalf("expected %q after byte %d in output:\n%s", want, pos, text)
		}
		pos += idx + len(want)
	}
}
