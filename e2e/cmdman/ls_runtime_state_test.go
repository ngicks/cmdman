package cmdman_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLs_SurfacesRuntimeState drives the whole path the runtime columns depend
// on: a command writes an OSC title and rings the bell on its own TTY, reports
// a status about itself through the status verb, and `ls` - a different process
// with no memory of any of it - dials the monitor and shows all three.
//
// The command needs a PTY (-t): without one there is no terminal emulator, so
// no callback ever sees the title or the bell.
func TestLs_SurfacesRuntimeState(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dir := t.TempDir()
	logPath := filepath.Join(dir, "report.log")
	script := filepath.Join(dir, "titled.sh")
	body := fmt.Sprintf(`#!/bin/sh
printf '\033]0;building the thing\007'
printf '\007'
%[1]q status set waiting --detail "needs input" > %[2]q 2>&1
echo "exit=$?" >> %[2]q
sleep 300
`, cmdmanBin, logPath)
	must(t, os.WriteFile(script, []byte(body), 0o755))

	env.run(ctx, "run", "-t", "-n", "titled", "--", "/bin/sh", script)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "titled") })
	env.waitForState(ctx, "titled", "running", defaultTimeout)

	// One poll for all four columns: the monitor debounces titles, so a single
	// read races the very thing under test.
	pollOutput(ctx, env, "building the thing|waiting|needs input|*",
		"ls", "--format", "{{.Title}}|{{.Status}}|{{.Detail}}|{{bell .BellUnread}}")

	// The child's own view of the report, so a child-side failure cannot hide
	// behind a column that stayed empty.
	report, err := os.ReadFile(logPath)
	must(t, err)
	if strings.TrimSpace(string(report)) != "exit=0" {
		t.Fatalf("supervised `status set` did not succeed; log:\n%s", report)
	}

	// Inspect carries the same state in its live-state section, from its own
	// dial: the one command's whole story, where ls shows one row of it.
	assertEq(t, env.run(
		ctx,
		"inspect",
		"titled",
		"--format",
		`{{with .LiveStatus.RuntimeState}}{{.Title}}|{{.Status}}|{{.Detail}}|{{.BellUnread}}{{end}}`,
	),
		"building the thing|waiting|needs input|true")

	// The default table carries the same facts under their headers.
	table := env.run(ctx, "ls")
	lines := strings.Split(table, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected a header and one row, got:\n%s", table)
	}
	for _, want := range []string{"STATUS", "DETAIL", "BELL", "TITLE"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header %q lacks the %s column", lines[0], want)
		}
	}
	for _, want := range []string{"waiting", "needs input", "building the thing"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("row %q lacks %q", lines[1], want)
		}
	}
}

// TestLs_RuntimeStateSurvivesMangledTitle drives a glyph title through the real
// binary: the unpatched parser cut "✳ done" (U+2733 = E2 9C B3) at the 0x9C —
// read as an 8-bit ST even mid-rune — and the invalid latched fragment failed
// proto marshaling of the whole Status response, listing as empty runtime
// columns while the attached terminal still showed the full title. With the
// vendored parser fix the title arrives whole; the latch-side sanitizing stays
// as the guard that keeps the row serving even if a parser hands over garbage
// again.
func TestLs_RuntimeStateSurvivesMangledTitle(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	dir := t.TempDir()
	script := filepath.Join(dir, "glyph.sh")
	body := `#!/bin/sh
printf '\033]0;plain title\007'
sleep 1
printf '\033]0;\342\234\263 done\007'
sleep 300
`
	must(t, os.WriteFile(script, []byte(body), 0o755))

	env.run(ctx, "run", "-t", "-n", "glyph-title", "--", "/bin/sh", script)
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "glyph-title") })
	env.waitForState(ctx, "glyph-title", "running", defaultTimeout)

	// The ASCII title first: the capture path works before the glyph arrives.
	pollOutput(ctx, env, "plain title", "ls", "--format", "{{.Title}}")

	// The vendored parser keeps the 0x9C continuation byte inside the OSC
	// string, so the glyph title arrives whole. Empty here is the original
	// bug: an invalid-UTF-8 latch killed the RPC and took every runtime
	// column with it; "�" would mean the parser regressed to cutting the
	// title and only the latch-side sanitizing held.
	pollOutput(ctx, env, "✳ done", "ls", "--format", "{{.Title}}")
}

// TestLs_RuntimeColumnsEmptyWithoutMonitor pins the other half of the contract:
// a command nobody is monitoring costs its columns, not the listing. A created
// command never had a monitor, and an exited one leaves its socket path behind
// for the dial to fail on lazily - both must list, quickly, with empty columns.
func TestLs_RuntimeColumnsEmptyWithoutMonitor(t *testing.T) {
	t.Parallel()
	ctx := testContext(t)
	env := newTestEnv(t)

	env.run(ctx, "create", "-n", "never-run", "--", "/bin/sh", "-c", "sleep 300")
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "never-run") })
	env.run(ctx, "run", "-n", "brief", "--", "/bin/sh", "-c", "exit 0")
	t.Cleanup(func() { env.cleanupCommand(context.Background(), "brief") })
	env.waitForState(ctx, "brief", "exited", defaultTimeout)

	stdout, stderr, err := env.exec(ctx, "ls", "-a", "--format",
		"{{.Name}}={{.Title}}/{{.Status}}/{{bell .BellUnread}}")
	if err != nil {
		t.Fatalf("ls with unreachable monitors failed: %v\n%s", err, stderr)
	}
	got := strings.Split(strings.TrimSpace(stdout), "\n")
	want := []string{"brief=//-", "never-run=//-"}
	if len(got) != len(want) {
		t.Fatalf("ls output = %q, want %d rows", stdout, len(want))
	}
	for _, line := range got {
		if !strings.Contains(strings.Join(want, " "), line) {
			t.Errorf("unexpected row %q, want one of %v", line, want)
		}
	}
}
