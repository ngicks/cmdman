package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLauncherWindowName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	testRoot := t.TempDir()
	tests := []struct {
		name    string
		workDir func(*testing.T) string
		want    string
	}{
		{
			name: "git repository and branch",
			workDir: func(t *testing.T) string {
				return launcherTestRepo(t, testRoot, "checkout", "main", "acme/repo.git")
			},
			want: "repo-main",
		},
		{
			name: "repository without origin",
			workDir: func(t *testing.T) string {
				return launcherTestRepo(t, testRoot, "local", "main", "")
			},
			want: "local-main",
		},
		{
			name: "non git directory",
			workDir: func(t *testing.T) string {
				return launcherTestDir(t, testRoot, "plain")
			},
			want: "plain",
		},
		{
			name: "git unavailable",
			workDir: func(t *testing.T) string {
				dir := launcherTestDir(t, testRoot, "missing-git")
				t.Setenv("PATH", "")
				return dir
			},
			want: "missing-g…",
		},
		{
			name: "detached head",
			workDir: func(t *testing.T) string {
				dir := launcherTestRepo(t, testRoot, "detached", "main", "acme/repo.git")
				runLauncherGit(t, dir, "checkout", "--detach")
				return dir
			},
			want: "detached",
		},
		{
			name: "slash bearing branch",
			workDir: func(t *testing.T) string {
				return launcherTestRepo(t, testRoot, "slash", "x/y", "acme/repo.git")
			},
			want: "repo-x/y",
		},
		{
			name: "exactly ten cells",
			workDir: func(t *testing.T) string {
				return launcherTestDir(t, testRoot, "abcdefghij")
			},
			want: "abcdefghij",
		},
		{
			name: "more than ten cells",
			workDir: func(t *testing.T) string {
				return launcherTestDir(t, testRoot, "abcdefghijk")
			},
			want: "abcdefghi…",
		},
		{
			name: "wide unicode",
			workDir: func(t *testing.T) string {
				return launcherTestDir(t, testRoot, "界界界界界界")
			},
			want: "界界界界…",
		},
		{
			name:    "empty work directory",
			workDir: func(*testing.T) string { return "" },
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := launcherWindowName(t.Context(), tt.workDir(t)); got != tt.want {
				t.Errorf("launcherWindowName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func launcherTestRepo(
	t *testing.T,
	root string,
	name string,
	branch string,
	origin string,
) string {
	t.Helper()
	dir := launcherTestDir(t, root, name)
	runLauncherGit(t, dir, "init", "-b", branch)
	if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runLauncherGit(t, dir, "add", "tracked")
	runLauncherGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "-m", "initial")
	if origin != "" {
		runLauncherGit(t, dir, "remote", "add", "origin", "git@github.com:"+origin)
	}
	return dir
}

func launcherTestDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runLauncherGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
