package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
)

// resolverFor pre-seeds the compose resolver so the merge can be exercised
// without compose files on disk: the classification of a file is what the
// resolver reports, and that is tested separately.
func resolverFor(entries map[string]resolvedCompose) *composeResolver {
	return &composeResolver{cache: entries}
}

func specWithMux(workDir, project string, hasMux bool) resolvedCompose {
	spec := &compose.ComposeSpec{WorkDir: workDir, Project: project}
	if hasMux {
		spec.Mux = &mux.Spec{}
	}
	return resolvedCompose{sel: compose.ProjectSelection{
		Spec: spec, WorkDir: workDir, Project: project,
	}}
}

// TestLaunchAccumulatorMergesByRecency covers D7's ordering and the merge: one
// location per work directory, most recently used first, history rows leading
// the projects at a location and carrying the from-history flag that makes them
// open enabled.
func TestLaunchAccumulatorMergesByRecency(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	acc := &launchAccumulator{}
	// History, newest first as the store returns it.
	acc.add("/w/api", "api", "/w/api/cmd-compose.yaml", now, true)
	acc.add("/w/web", "web", "/w/web/cmd-compose.yaml", now.Add(-time.Hour), true)
	// The store listing repeats one of them and adds a co-located project.
	acc.add("/w/api", "api", "/w/api/cmd-compose.yaml", time.Time{}, false)
	acc.add("/w/api", "tools", "/w/api/tools.yaml", time.Time{}, false)
	// A named def nobody has run: no history, so it sorts after the history rows.
	acc.add("/w/never", "named", "/w/never/compose.yaml", time.Time{}, false)

	locs := acc.locations(resolverFor(map[string]resolvedCompose{
		"/w/api/cmd-compose.yaml": specWithMux("/w/api", "api", true),
		"/w/api/tools.yaml":       specWithMux("/w/api", "tools", false),
		"/w/web/cmd-compose.yaml": specWithMux("/w/web", "web", true),
		"/w/never/compose.yaml":   specWithMux("/w/never", "named", true),
	}), nil)

	var dirs []string
	for _, l := range locs {
		dirs = append(dirs, l.Dir)
	}
	if got, want := strings.Join(dirs, ","), "/w/api,/w/web,/w/never"; got != want {
		t.Fatalf("location order = %s, want %s", got, want)
	}
	if !locs[0].FromHistory || !locs[0].LastUsed.Equal(now) {
		t.Errorf("the history stamp should reach the location: %+v", locs[0])
	}
	if locs[2].FromHistory {
		t.Errorf("a never-run location must not claim history")
	}

	api := locs[0].Projects
	if len(api) != 2 || api[0].Name != "api" || api[1].Name != "tools" {
		t.Fatalf("projects at /w/api = %+v, want api then tools", api)
	}
	if !api[0].FromHistory || api[1].FromHistory {
		t.Errorf("only the history project should be flagged: %+v", api)
	}
	if !api[0].HasMux || api[1].HasMux {
		t.Errorf("the mux: section should be read per project: %+v", api)
	}
}

// TestLaunchRunningIdentityUsesComposeWorkdir is the check that a fake cannot
// fake: the identity the launcher derives from a history row must be the very
// string mux stamps on the project's window, or every running project would
// render cold. Both sides are computed here from the same compose helpers, the
// launcher's from the (WorkDir, Project) pair the history table stores.
func TestLaunchRunningIdentityUsesComposeWorkdir(t *testing.T) {
	spec := &compose.ComposeSpec{WorkDir: "/w/api", Project: "api"}
	stamped := compose.SelectionFromSpec(spec).ProjectIdentity()
	if stamped == "" {
		t.Fatal("compose stamps an empty identity; the test is meaningless")
	}

	acc := &launchAccumulator{}
	acc.add(spec.WorkDir, spec.Project, "/w/api/cmd-compose.yaml", time.Now(), true)
	acc.add("/w/api", "tools", "/w/api/tools.yaml", time.Time{}, false)

	locs := acc.locations(resolverFor(map[string]resolvedCompose{
		"/w/api/cmd-compose.yaml": specWithMux("/w/api", "api", true),
		"/w/api/tools.yaml":       specWithMux("/w/api", "tools", true),
	}), map[string]bool{stamped: true})

	if !locs[0].Projects[0].Running {
		t.Errorf("the project whose window is stamped %q should read as running", stamped)
	}
	if locs[0].Projects[1].Running {
		t.Errorf("a project with no window of its own must not read as running")
	}
}

// TestComposeResolverClassify covers the stale surface (D10): a compose file
// that is gone is the removable case, one that fails to load for any other
// reason blocks the launch just as hard but is not a history row to forget.
func TestComposeResolverClassify(t *testing.T) {
	r := resolverFor(map[string]resolvedCompose{
		"/w/ok/compose.yaml":    specWithMux("/w/ok", "ok", true),
		"/w/nomux/compose.yaml": specWithMux("/w/nomux", "nomux", false),
		"/w/gone/compose.yaml": {
			err: fmt.Errorf("open /w/gone/compose.yaml: %w", fs.ErrNotExist),
		},
		"/w/broken/compose.yaml": {err: fmt.Errorf("decode /w/broken/compose.yaml: bad yaml")},
		"unresolvable":           {},
	})

	for _, tc := range []struct {
		file        string
		hasMux      bool
		wantProblem string
		missing     bool
	}{
		{file: "/w/ok/compose.yaml", hasMux: true},
		{file: "/w/nomux/compose.yaml"},
		{
			file: "/w/gone/compose.yaml", missing: true,
			wantProblem: "missing: /w/gone/compose.yaml",
		},
		{file: "/w/broken/compose.yaml", wantProblem: "decode /w/broken/compose.yaml: bad yaml"},
		{file: "unresolvable", missing: true, wantProblem: "missing: no compose file found"},
	} {
		hasMux, problem, missing := r.classify(tc.file, "project")
		if hasMux != tc.hasMux {
			t.Errorf("%s: hasMux = %v, want %v", tc.file, hasMux, tc.hasMux)
		}
		if missing != tc.missing {
			t.Errorf("%s: missing = %v, want %v", tc.file, missing, tc.missing)
		}
		switch {
		case tc.wantProblem == "" && problem != "":
			// A resolvable file has nothing to say; HasPrefix would accept anything.
			t.Errorf("%s: problem = %q, want none", tc.file, problem)
		case tc.wantProblem != "" && !strings.HasPrefix(problem, tc.wantProblem):
			t.Errorf("%s: problem = %q, want it to start with %q", tc.file, problem, tc.wantProblem)
		}
	}
}

// TestUpChangedCommands pins when a landing rebuilds the dashboard: a create or
// a recreate hands the command a new id, so the panes viewing the old one are
// stale; anything else leaves a running dashboard alone (D4's idempotent `S`).
func TestUpChangedCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		res     *compose.UpResult
		changed bool
	}{
		{name: "nothing reported", res: nil, changed: true},
		{name: "all unchanged", res: upResult("unchanged", "unchanged")},
		{name: "one created", res: upResult("unchanged", "create"), changed: true},
		{name: "one recreated", res: upResult("recreate"), changed: true},
		{name: "orphan removed only", res: upResult("unchanged", "remove-orphan")},
	} {
		if got := upChangedCommands(tc.res); got != tc.changed {
			t.Errorf("%s: upChangedCommands = %v, want %v", tc.name, got, tc.changed)
		}
	}
}

// TestUpVerdictReadsTheResult pins what makes a bring-up a failure: compose
// aggregates per-command failures into the result and returns no error of its
// own, so a landing that only looked at the returned error would report success
// and put the user in front of a dashboard of dead panes.
func TestUpVerdictReadsTheResult(t *testing.T) {
	target := tui.LaunchTarget{WorkDir: "/w/api", Project: "api"}
	boom := errors.New("boom")

	if err := upVerdict(target, upResult("create", "unchanged"), nil); err != nil {
		t.Errorf("a clean bring-up should be no error, got %v", err)
	}
	for name, res := range map[string]*compose.UpResult{
		"a command would not be created": {
			CreateResult: compose.CreateResult{
				Actions: []compose.ActionOutcome{{Command: "api", Action: "create", Err: boom}},
			},
		},
		"a command would not start": {
			Starts: []compose.StartOutcome{{Command: "api", Err: boom}},
		},
	} {
		err := upVerdict(target, res, nil)
		if err == nil {
			t.Errorf("%s: bring-up reported success", name)
			continue
		}
		// UpResultErr counts the failures rather than wrapping them (the CLI
		// prints them through the progress reporter); what the launcher needs is
		// a failure attributed to the project it could not bring up.
		if !strings.Contains(err.Error(), "up api") {
			t.Errorf("%s: error = %v, want it to name the project", name, err)
		}
	}

	// The call's own error still wins, and a result-less failure is not a nil
	// dereference.
	if err := upVerdict(target, nil, boom); !errors.Is(err, boom) {
		t.Errorf("a failed up call = %v, want it to carry %v", err, boom)
	}
}

func upResult(actions ...string) *compose.UpResult {
	res := &compose.UpResult{}
	for _, a := range actions {
		res.Actions = append(res.Actions, compose.ActionOutcome{Command: "c", Action: a})
	}
	return res
}

func TestRepoNameFromURI(t *testing.T) {
	for uri, want := range map[string]string{
		"https://github.com/ngicks/cmdman.git": "cmdman",
		"https://github.com/ngicks/cmdman":     "cmdman",
		"git@github.com:acme/webapp.git":       "webapp",
		"/srv/git/bare/edge.git/":              "edge",
		"cmdman":                               "cmdman",
	} {
		if got := repoNameFromURI(uri); got != want {
			t.Errorf("repoNameFromURI(%q) = %q, want %q", uri, got, want)
		}
	}
}
