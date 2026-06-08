package cmdman_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty/v2"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

// parseJSONArray decodes a JSON array document into a slice of generic objects.
func parseJSONArray(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &arr); err != nil {
		t.Fatalf("parse JSON array: %v\nraw:\n%s", err, raw)
	}
	return arr
}

// composeTTYReadYAML defines two TTY commands that each block on a single line
// of stdin, print a fixed marker, then idle. Markers are constant strings (no
// shell variables) so compose's args interpolation leaves them intact.
func composeTTYReadYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    tty: true
    args: [sh, -c, "read _; echo ALPHA_RX; sleep 30"]
  beta:
    tty: true
    args: [sh, -c, "read _; echo BETA_RX; sleep 30"]
`, name)
}

// composeTTYSleepYAML defines a single long-lived TTY command, suitable for
// attach tests.
func composeTTYSleepYAML(name string) string {
	return fmt.Sprintf(`name: %s
commands:
  alpha:
    tty: true
    args: [sleep, "300"]
`, name)
}

// TestComposeFormatJSON verifies --format json and Go-template output for
// compose ps and compose ls.
func TestComposeFormatJSON(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-fmt-json"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
		}
	}

	// ps --format json: an array of command statuses with CamelCase keys.
	psArr := parseJSONArray(t,
		env.run(ctx, "compose", "--workdir", wd, "-f", composePath, "ps", "--format", "json"))
	if len(psArr) != 2 {
		t.Fatalf("expected 2 ps entries, got %d: %#v", len(psArr), psArr)
	}
	states := map[string]string{}
	for _, e := range psArr {
		cmd, _ := e["Command"].(string)
		st, _ := e["State"].(string)
		states[cmd] = st
	}
	if states["alpha"] != "exited" || states["beta"] != "exited" {
		t.Fatalf("expected alpha/beta exited in ps json; got %#v", states)
	}

	// ps --format <template>: a Go text/template applied per row.
	tmplOut := env.run(ctx, "compose", "--workdir", wd, "-f", composePath,
		"ps", "--format", "{{.Command}}={{.State}}")
	if !strings.Contains(tmplOut, "alpha=exited") || !strings.Contains(tmplOut, "beta=exited") {
		t.Fatalf("expected templated ps rows; got:\n%s", tmplOut)
	}

	// ls --format json: an array of project summaries.
	lsArr := parseJSONArray(t, env.run(ctx, "compose", "ls", "--format", "json"))
	found := false
	for _, p := range lsArr {
		if p["Project"] == project {
			found = true
			if got, _ := p["Commands"].(float64); got != 2 {
				t.Fatalf("expected 2 commands for project, got %v", p["Commands"])
			}
		}
	}
	if !found {
		t.Fatalf("project %q not in compose ls json; got:\n%#v", project, lsArr)
	}
}

// TestComposeInspect verifies compose inspect returns a JSON array for the whole
// project and narrows to a single command when filtered.
func TestComposeInspect(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-inspect"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "exited", 5*time.Second)
		}
	}

	// Default (no --format): a JSON array of every project command's inspect output.
	allArr := parseJSONArray(t,
		env.run(ctx, "compose", "--workdir", wd, "-f", composePath, "inspect"))
	if len(allArr) != 2 {
		t.Fatalf("expected 2 inspect outputs, got %d: %#v", len(allArr), allArr)
	}
	for _, o := range allArr {
		if _, ok := o["Config"]; !ok {
			t.Fatalf("inspect output missing config field: %#v", o)
		}
	}

	// Filtered to alpha: a single-element array.
	oneArr := parseJSONArray(t,
		env.run(ctx, "compose", "--workdir", wd, "-f", composePath, "inspect", "alpha"))
	if len(oneArr) != 1 {
		t.Fatalf("expected 1 inspect output for alpha, got %d: %#v", len(oneArr), oneArr)
	}
	if name, _ := oneArr[0]["Name"].(string); !strings.Contains(name, "alpha") {
		t.Fatalf("expected alpha in inspect name; got %q", name)
	}
}

// TestComposeEvents verifies compose events --no-follow replays event types for
// the project's commands and filters out unrelated commands.
func TestComposeEvents(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-events"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	// A standalone command sharing the event log; its events must be filtered out.
	unrelatedID := env.run(ctx, "run", "-n", "tc-events-unrelated", "--", "/bin/sh", "-c", "exit 0")
	t.Cleanup(func() { env.cleanupCommand(ctx, unrelatedID) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	ids := map[string]string{}
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			id := e["ID"].(string)
			ids[name] = id
			env.waitForState(ctx, id, "exited", 5*time.Second)
		}
	}

	out := env.run(ctx, "compose", "--workdir", wd, "-f", composePath, "events", "--no-follow")
	gotTypes := collectEventTypes(t, out)
	for _, w := range []string{"created", "running", "exited"} {
		if _, ok := gotTypes[w]; !ok {
			t.Fatalf("expected event type %q in compose events; got %v\nraw:\n%s",
				w, sortedKeys(gotTypes), out)
		}
	}
	for name, id := range ids {
		if !strings.Contains(out, id) {
			t.Fatalf("expected %s id %q in compose events output:\n%s", name, id, out)
		}
	}
	if strings.Contains(out, unrelatedID) {
		t.Fatalf("unrelated command %q leaked into compose events output:\n%s", unrelatedID, out)
	}
}

// TestComposeSendKeys verifies targeted and broadcast send-keys against running
// compose command PTYs.
func TestComposeSendKeys(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-sendkeys"
	writeComposeFile(t, wd, composeTTYReadYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		for _, e := range env.lsJSON(ctx,
			"-l", "cmdman.compose.command="+name,
			"-l", "cmdman.compose.project="+project,
		) {
			env.waitForState(ctx, e["ID"].(string), "running", 5*time.Second)
		}
	}

	// Targeted: send only to alpha. Its read unblocks and prints ALPHA_RX.
	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"send-keys", "alpha", "--", "Enter")
	if err != nil {
		t.Fatalf(
			"compose send-keys alpha failed: %v\nstdout:\n%s\nstderr:\n%s",
			err,
			stdout,
			stderr,
		)
	}
	if !strings.Contains(stdout, "sent         alpha") {
		t.Fatalf("expected sent line for alpha; got:\n%s", stdout)
	}
	if strings.Contains(stdout, "beta") {
		t.Fatalf("targeted send-keys must not touch beta; got:\n%s", stdout)
	}
	waitUntil(t, defaultTimeout, func() bool {
		out, _, e := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "logs", "alpha")
		return e == nil && strings.Contains(out, "ALPHA_RX")
	}, "ALPHA_RX did not appear after targeted send-keys")

	// beta is still blocked on read; its marker must not have printed.
	betaLogs, _, _ := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "logs", "beta")
	if strings.Contains(betaLogs, "BETA_RX") {
		t.Fatalf("beta received input from a targeted alpha send-keys:\n%s", betaLogs)
	}

	// Broadcast: no command filter sends to every command, unblocking beta.
	bStdout, bStderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"send-keys", "--", "Enter")
	if err != nil {
		t.Fatalf("compose send-keys broadcast failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, bStdout, bStderr)
	}
	if !strings.Contains(bStdout, "sent         beta") {
		t.Fatalf("expected sent line for beta on broadcast; got:\n%s", bStdout)
	}
	waitUntil(t, defaultTimeout, func() bool {
		out, _, e := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "logs", "beta")
		return e == nil && strings.Contains(out, "BETA_RX")
	}, "BETA_RX did not appear after broadcast send-keys")
}

// TestComposeSendKeysRequiresSeparator verifies send-keys errors when the `--`
// separator between command names and keys is missing.
func TestComposeSendKeysRequiresSeparator(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-sk-sep"
	writeComposeFile(t, wd, composeBasicYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(
		ctx,
		"compose",
		"--workdir",
		wd,
		"-f",
		composePath,
		"create",
	); err != nil {
		t.Fatalf("compose create failed: %v", err)
	}

	stdout, stderr, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath,
		"send-keys", "alpha", "Enter")
	if err == nil {
		t.Fatalf("send-keys without `--` should fail; stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "--") {
		t.Fatalf("expected error mentioning the `--` separator; got:\n%s\n%s", stdout, stderr)
	}
}

// TestComposeAttachDetach attaches to a running compose command's PTY, sends the
// detach key sequence, and verifies the attach process exits while the command
// keeps running.
func TestComposeAttachDetach(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-attach"
	writeComposeFile(t, wd, composeTTYSleepYAML(project))
	t.Cleanup(func() { cleanupProject(ctx, env, wd, project) })

	composePath := filepath.Join(wd, "cmd-compose.yaml")
	if _, _, err := env.exec(ctx, "compose", "--workdir", wd, "-f", composePath, "up"); err != nil {
		t.Fatalf("compose up failed: %v", err)
	}
	var alphaID string
	for _, e := range env.lsJSON(ctx, "-l", "cmdman.compose.project="+project) {
		alphaID = e["ID"].(string)
	}
	if alphaID == "" {
		t.Fatal("alpha command not found after compose up")
	}
	env.waitForState(ctx, alphaID, "running", defaultTimeout)

	attach := exec.CommandContext(ctx, cmdmanBin,
		"compose", "--workdir", wd, "-f", composePath, "attach", "alpha")
	attach.Env = append(
		os.Environ(),
		cmdman.ENV_CMDMAN_DATA_DIR+"="+env.dataHome,
		cmdman.ENV_CMDMAN_RUNTIME_DIR+"="+env.runtimeDir,
	)

	ptmx, err := pty.Start(attach)
	if err != nil {
		t.Fatalf("start compose attach pty: %v", err)
	}
	defer ptmx.Close()
	answerTerminalProbes(ptmx, nil)

	time.Sleep(300 * time.Millisecond)
	if _, err := ptmx.Write([]byte{0x10, 0x11}); err != nil {
		t.Fatalf("send detach keys: %v", err)
	}

	waitAttachExit(t, attach, 3*time.Second)
	// The command must still be running after a detach.
	env.waitForState(ctx, alphaID, "running", defaultTimeout)
}
