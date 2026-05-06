package cmdman_test

import (
	"context"
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

func TestLogDriver_K8sFileWritesLogToCommandDir(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "run", "-n", "k8s-log-default", "--", "/bin/sh", "-c",
		"echo 'k8s-driver-line-A'; echo 'k8s-driver-line-B'")
	t.Cleanup(func() { env.cleanupCommand(ctx, "k8s-log-default") })
	env.waitForState(ctx, "k8s-log-default", "exited", defaultTimeout)

	info := env.inspectJSON(ctx, "k8s-log-default")
	hexID, _ := info["id"].(string)
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
	hexID, _ := info["id"].(string)
	if hexID == "" {
		t.Fatalf("inspect returned empty id; raw=%v", info)
	}
	logPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("expected no log file at %s, got err=%v", logPath, err)
	}

	// The configured driver should round-trip into inspect output.
	cfg, _ := info["config"].(map[string]any)
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
	hexID, _ := info["id"].(string)
	defaultPath := filepath.Join(env.dataHome, "commands", hexID, "console.log")
	if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
		t.Errorf(
			"default log path %s should not exist when path opt is set; err=%v",
			defaultPath,
			err,
		)
	}

	cfg, _ := info["config"].(map[string]any)
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
