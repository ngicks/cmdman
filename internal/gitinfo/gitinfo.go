// Package gitinfo discovers the Git identity of a working directory.
package gitinfo

import (
	"context"
	"os/exec"
	"path"
	"strings"
	"time"
)

// Info describes the repository and branch containing a working directory.
type Info struct {
	RepoName string
	RepoURI  string
	Branch   string
}

// probeTimeout bounds one directory's probe. Git is normally instant, but a
// work tree on a stalled network mount must not hold the caller.
const probeTimeout = 2 * time.Second

// Probe reads a directory's Git identity. Every failure is silent:
// "not a repository", "no remote", and "git is not installed" all produce
// whatever information can be discovered, or an empty Info.
func Probe(ctx context.Context, dir string) Info {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// One call provides the facts that come from the work tree itself. The
	// top-level directory also names the repository when no origin is present.
	out, err := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD", "--show-toplevel")
	if err != nil {
		return Info{}
	}
	fields := strings.Split(out, "\n")
	if len(fields) < 2 {
		return Info{}
	}
	info := Info{Branch: fields[0], RepoName: path.Base(fields[1])}
	if uri, err := gitOutput(ctx, dir, "remote", "get-url", "origin"); err == nil {
		info.RepoURI = uri
		info.RepoName = RepoNameFromURI(uri)
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

// RepoNameFromURI returns the last path segment of a remote URI without its
// .git suffix. Both URL and scp-like forms are supported.
func RepoNameFromURI(uri string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(uri, "/"), ".git"), "/")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return name
}
