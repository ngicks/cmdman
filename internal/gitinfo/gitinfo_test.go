package gitinfo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/internal/gitinfo"
)

func TestRepoNameFromURI(t *testing.T) {
	for uri, want := range map[string]string{
		"https://github.com/ngicks/cmdman.git": "cmdman",
		"https://github.com/ngicks/cmdman":     "cmdman",
		"git@github.com:acme/webapp.git":       "webapp",
		"/srv/git/bare/edge.git/":              "edge",
		"cmdman":                               "cmdman",
	} {
		if got := gitinfo.RepoNameFromURI(uri); got != want {
			t.Errorf("RepoNameFromURI(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestProbe(t *testing.T) {
	repo := initRepository(t, "feature/launcher")
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	info := gitinfo.Probe(t.Context(), nested)
	if info.RepoName != filepath.Base(repo) || info.RepoURI != "" ||
		info.Branch != "feature/launcher" {
		t.Errorf("Probe without origin = %+v, want top-level basename and branch", info)
	}

	runGit(t, repo, "remote", "add", "origin", "git@github.com:acme/renamed.git")
	info = gitinfo.Probe(t.Context(), nested)
	if info.RepoName != "renamed" || info.RepoURI != "git@github.com:acme/renamed.git" ||
		info.Branch != "feature/launcher" {
		t.Errorf("Probe with origin = %+v, want origin-derived name, URI, and branch", info)
	}
}

func TestProbeOutsideRepository(t *testing.T) {
	if info := gitinfo.Probe(t.Context(), t.TempDir()); info != (gitinfo.Info{}) {
		t.Errorf("Probe outside repository = %+v, want empty info", info)
	}
}

func initRepository(t *testing.T, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", branch)
	file := filepath.Join(repo, "tracked")
	if err := os.WriteFile(file, []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "tracked")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
