package core

import "time"

// LaunchTarget identifies one compose project for a launcher action. WorkDir is
// the canonical project directory (filepath.Clean(filepath.Abs(p)), no symlink
// resolution — the form compose project identity and the history table share),
// Project the compose project name and File the compose file it comes from.
type LaunchTarget struct {
	WorkDir string
	Project string
	File    string
}

// LaunchProject is one compose project at a location: what the launcher's right
// pane toggles and acts on.
type LaunchProject struct {
	// Name is the compose project name.
	Name string
	// File is the compose file the project comes from.
	File string
	// FromHistory reports that the project was brought up before, which is what
	// makes it enabled on open.
	FromHistory bool
	// FromConfig reports that the project was discovered via the compose config
	// dir (compose.ListNamedProjects), which is what makes it a project the user
	// keeps around rather than one that merely happens to sit in a directory.
	FromConfig bool
	// Running reports that a mux window carrying this project's identity exists.
	Running bool
	// HasMux reports that the compose file declares a mux: section. A project
	// without one is brought up but has no dashboard to land in (D9).
	HasMux bool
	// Problem describes why the project cannot be brought up at all, "" when it
	// can. It is shown inline under the row rather than hidden until the launch
	// fails (D10).
	Problem string
	// Missing reports that Problem is a compose file that no longer exists — the
	// stale-history case whose answer is removal.
	Missing bool
}

// LaunchLocation is one target directory: what the launcher's left pane selects.
// The git fields are D18's display and match keys; they are empty for a
// directory that is not a git work tree.
type LaunchLocation struct {
	// Dir is the canonical project directory (see LaunchTarget.WorkDir).
	Dir string
	// RepoName is the git repository name, "" outside a work tree.
	RepoName string
	// RepoURI is the git remote uri, "" when there is no remote.
	RepoURI string
	// Branch is the checked-out branch, "" outside a work tree.
	Branch string
	// LastUsed is the most recent history stamp among the location's projects;
	// the zero time means the location was never launched from.
	LastUsed time.Time
	// FromHistory reports that at least one project here is known from history,
	// which is what the empty-input history list shows.
	FromHistory bool
	// FromConfig reports that at least one project here came from the compose
	// config dir.
	FromConfig bool
	// Projects are the compose projects at this location.
	Projects []LaunchProject
}

// LaunchOutcome is what a landing has to say for itself beyond "it worked". A
// zero value means the caller is looking at the project already, which is the
// everyday case and the one that dismisses the launcher (D10).
type LaunchOutcome struct {
	// Warning is a non-fatal note the user should read: a project with no mux:
	// section is up and landed on a bare shell window, and the warning is what
	// explains why the window looks empty (D9). The launcher stays open to show
	// it — the landing itself already happened underneath.
	Warning string
	// AttachCommand is the argv that gives this terminal to the multiplexer,
	// set when the launcher was summoned from outside it: no client exists to
	// switch, so landing means becoming one (D8). The launcher runs it with the
	// terminal released and dismisses when it returns.
	AttachCommand []string
}

// Landed reports the boring outcome: nothing to say, nothing to run.
func (o LaunchOutcome) Landed() bool {
	return o.Warning == "" && len(o.AttachCommand) == 0
}
