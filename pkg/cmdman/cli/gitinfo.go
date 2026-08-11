package cli

import (
	"context"
	"os/exec"
	"path"
	"strings"
	"time"
)

// gitInfo is what the launcher shows and matches a location on (D18): the
// repository name, its remote uri and the checked-out branch. Every field is
// empty for a directory that is not a git work tree, which is not an error — the
// launcher falls back to the directory basename.
type gitInfo struct {
	RepoName string
	RepoURI  string
	Branch   string
}

// gitProbeTimeout bounds one directory's probe. git is normally instant, but a
// work tree on a stalled network mount must not hold the launcher's listing.
const gitProbeTimeout = 2 * time.Second

// probeGit reads a directory's git identity by running git in it (D41): always
// correct, including worktrees, submodules and gitdir indirection, at the cost
// of two execs per entry at launcher-open time. Every failure is silent: "not a
// repository", "no remote" and "git is not installed" are all just an entry with
// no git info.
func probeGit(ctx context.Context, dir string) gitInfo {
	ctx, cancel := context.WithTimeout(ctx, gitProbeTimeout)
	defer cancel()

	// One call for the two facts that come from the work tree itself; the
	// toplevel doubles as the repo name when there is no remote to name it.
	out, err := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD", "--show-toplevel")
	if err != nil {
		return gitInfo{}
	}
	fields := strings.Split(out, "\n")
	if len(fields) < 2 {
		return gitInfo{}
	}
	info := gitInfo{Branch: fields[0], RepoName: path.Base(fields[1])}
	if uri, err := gitOutput(ctx, dir, "remote", "get-url", "origin"); err == nil {
		info.RepoURI = uri
		info.RepoName = repoNameFromURI(uri)
	}
	return info
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// repoNameFromURI is the last path segment of a remote uri without its .git
// suffix, which is the name a human calls the repository. It handles the scp-ish
// form (git@host:owner/repo.git) as well as urls, since both are just
// slash-separated tails.
func repoNameFromURI(uri string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(uri, "/"), ".git"), "/")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return name
}
