//go:build linux

package cmdman_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestComposeAttach_ChdirsIntoCommandDir proves the viewer half of the pane
// path for compose: `compose attach` moves the attaching process into the
// directory its command is configured to run in, so a multiplexer asked for the
// pane's path answers with the command's directory and not with wherever the
// attach happened to be typed.
//
// The check reads the attach process's own cwd through /proc rather than
// anything it prints: the chdir leaves no output, and the process under test is
// the one the pty was started for.
func TestComposeAttach_ChdirsIntoCommandDir(t *testing.T) {
	ctx := testContext(t)
	env := newTestEnv(t)
	wd := composeWorkdir(t)
	project := "tc-attach-cwd"
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

	// Asking the command itself keeps the test on the viewer's behavior: which
	// directory compose gives a command that names none is compose's business.
	stdout, _, err := env.exec(ctx, "inspect", "--format", "{{.Config.Dir}}", alphaID)
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	want := resolvedPath(t, strings.TrimSpace(stdout))
	if want == "" {
		t.Fatal("the compose command has no configured directory to chdir into")
	}

	attach := env.Cmd("compose", "--workdir", wd, "-f", composePath, "attach", "alpha").
		StartPTY(ctx, t)

	waitUntil(t, defaultTimeout, func() bool {
		cwd, err := os.Readlink("/proc/" + strconv.Itoa(attach.Pid()) + "/cwd")
		return err == nil && cwd == want
	}, "compose attach never moved into %q", want)

	detachAttach(t, attach)
}

// resolvedPath restates dir the way /proc reports a cwd: fully resolved, so a
// temp root reached through a symlink compares equal to the link target the
// kernel hands back.
func resolvedPath(t *testing.T, dir string) string {
	t.Helper()
	if dir == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %q: %v", dir, err)
	}
	return resolved
}
