package cmdman_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These cover the `cmdman mux frame` CLI surface only: the help text the verbs
// publish and the errors they produce with no window to act on. The frame
// lifecycle itself (show → up → cycle → down → hide with managed entries
// surviving) is exercised against a real tmux server elsewhere.
//
// Every invocation here runs with $TMUX / $ZELLIJ stripped and TMUX_TMPDIR
// pointed at the test's own directory (muxExecWithTmpdir), so a frame verb can
// never reach the tmux server the developer is sitting in: the verbs take no
// spec path, so they cannot be pointed at a dedicated -L socket the way the
// other mux e2e tests are.

// writeFrameDef writes a one-entry def named <name>.yaml for the tests that only
// need a def to exist, and returns the def name.
func writeFrameDef(t *testing.T, e *testEnv, name string) string {
	t.Helper()
	return writeFrameDefFile(
		t, e, name,
		"frame:\n  - edge: bottom\n    size: 2\n    component: switcher\n",
	)
}

// writeFrameDefFile writes content as the def named <name>.yaml into the frame
// dir the test env resolves ($CMDMAN_CONF's directory + /frame) and returns the
// def name.
func writeFrameDefFile(t *testing.T, e *testEnv, name, content string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(e.confPath), "frame")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644))
	return name
}

// helpFlagNames returns the flag lines of a command's own "Flags:" block (the
// inherited "Global Flags:" block is a separate one and is not the command's
// surface), trimmed of their usage text.
func helpFlagNames(help string) []string {
	_, rest, ok := strings.Cut(help, "Flags:\n")
	if !ok {
		return nil
	}
	block, _, _ := strings.Cut(rest, "\nGlobal Flags:")
	var out []string
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-") {
			out = append(out, line)
		}
	}
	return out
}

func TestMuxFrame_HelpListsTheVerbs(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	out := env.run(ctx, "mux", "frame", "--help")
	for _, verb := range []string{"show", "hide", "cycle", "ls"} {
		if !strings.Contains(out, verb) {
			t.Fatalf("mux frame --help does not list %q:\n%s", verb, out)
		}
	}

	// -s/--session plus --help is the whole flag set of every frame verb (the
	// family inherits the session flag from mux); anything else appearing here
	// is a change to the published surface, so the count is asserted, not just
	// the presence.
	for _, verb := range []string{"show", "hide", "cycle", "ls"} {
		out := env.run(ctx, "mux", "frame", verb, "--help")
		flags := helpFlagNames(out)
		if len(flags) != 2 || !strings.Contains(flags[0], "--help") ||
			!strings.Contains(flags[1], "-s, --session") {
			t.Fatalf("mux frame %s has flags %q, want only --help and --session:\n%s",
				verb, flags, out)
		}
	}
	if out := env.run(ctx, "mux", "frame", "show", "--help"); !strings.Contains(out, "[DEF]") {
		t.Fatalf("mux frame show --help does not show the optional DEF argument:\n%s", out)
	}
}

// TestMuxFrame_NoDefNamedListsCandidates: with no DEF and no default_frame,
// show names what is on disk rather than guessing a def.
func TestMuxFrame_NoDefNamedListsCandidates(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	ctx := context.Background()

	_, stderr, err := env.muxExecWithTmpdir(ctx, t.TempDir(), "mux", "frame", "show")
	if err == nil {
		t.Fatal("mux frame show with no def and no default_frame succeeded")
	}
	if !strings.Contains(stderr, "no defs found") {
		t.Fatalf("unexpected error for an empty frame dir: %q", stderr)
	}

	writeFrameDef(t, env, "dev")
	_, stderr, err = env.muxExecWithTmpdir(ctx, t.TempDir(), "mux", "frame", "show")
	if err == nil {
		t.Fatal("mux frame show with no def and no default_frame succeeded")
	}
	if !strings.Contains(stderr, "available defs: dev") {
		t.Fatalf("error does not name the candidate defs: %q", stderr)
	}
}

// TestMuxFrame_OutsideMultiplexerErrors: a frame is a per-window fixture, so
// with no window to point at the verbs say so plainly instead of guessing one
// or leaking driver detail.
func TestMuxFrame_OutsideMultiplexerErrors(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	env := newTestEnv(t)
	ctx := context.Background()
	def := writeFrameDef(t, env, "dev")
	tmpdir := t.TempDir()

	for _, args := range [][]string{
		{"mux", "frame", "show", def},
		{"mux", "frame", "hide"},
		{"mux", "frame", "cycle"},
	} {
		_, stderr, err := env.muxExecWithTmpdir(ctx, tmpdir, args...)
		if err == nil {
			t.Fatalf("%v outside a multiplexer succeeded", args)
		}
		if !strings.Contains(stderr, "not inside a multiplexer") {
			t.Fatalf("%v: unexpected error: %q", args, stderr)
		}
	}
}

// TestMuxFrameLs_ListsDefsWithNoFrameUp: ls is server-wide, so it answers
// outside the multiplexer too — the defs on disk with no window showing them.
func TestMuxFrameLs_ListsDefsWithNoFrameUp(t *testing.T) {
	t.Parallel()
	requireTmux(t)
	env := newTestEnv(t)
	ctx := context.Background()
	writeFrameDef(t, env, "alt")
	writeFrameDef(t, env, "dev")

	stdout, stderr, err := env.muxExecWithTmpdir(ctx, t.TempDir(), "mux", "frame", "ls")
	if err != nil {
		t.Fatalf("mux frame ls: %v\nstderr: %s", err, stderr)
	}
	lines := strings.Split(stdout, "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected mux frame ls output:\n%s", stdout)
	}
	if !strings.HasPrefix(lines[0], "DEF") || !strings.Contains(lines[0], "WINDOWS") {
		t.Fatalf("missing header row:\n%s", stdout)
	}
	for i, want := range []string{"alt", "dev"} {
		fields := strings.Fields(lines[i+1])
		if len(fields) != 2 || fields[0] != want || fields[1] != "-" {
			t.Fatalf("row %d is not %q shown nowhere: %q", i+1, want, lines[i+1])
		}
	}
}
