package launcher

import (
	"io/fs"
	"os"
	"strings"
)

// homeToken is the home spelling the input carries — "~" or "$HOME" — and only
// on a path-component boundary: "~X" and "$HOMEX" name somewhere else entirely,
// the same rule core.AbbrevHome writes the abbreviation under.
func homeToken(p string) (string, bool) {
	for _, tok := range []string{"~", "$HOME"} {
		after, ok := strings.CutPrefix(p, tok)
		if !ok || (after != "" && after[0] != '/') {
			continue
		}
		return tok, true
	}
	return "", false
}

// expandHome is core.AbbrevHome's inverse: what the input means when it is read as a
// path. The filter itself keeps the user's spelling — the pane displays paths
// abbreviated, so that is how they get typed back in — while matching and every
// filesystem call work on the expanded form.
func expandHome(p, home string) string {
	tok, ok := homeToken(p)
	if !ok || home == "" {
		return p
	}
	return home + p[len(tok):]
}

// pathShaped reports that the input is a path being typed rather than a fuzzy
// query. Only a path reaches the filesystem: a bare word is a search over the
// listing, and reading a directory for it would complete toward paths the user
// never named.
func pathShaped(p string) bool {
	if _, ok := homeToken(p); ok {
		return true
	}
	return strings.HasPrefix(p, "/") ||
		strings.HasPrefix(p, "./") ||
		strings.HasPrefix(p, "../")
}

// fsDirCandidates lists the directories on disk that extend p: its directory
// portion read once, filtered by what has been typed of the last component.
// Directories only — the launcher selects a location, and a compose file is not
// one.
//
// A read that fails is no candidates rather than an error: on most keystrokes a
// path being typed names nothing yet, and that is not something to report.
func fsDirCandidates(p string) []string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return nil
	}
	dir, base := p[:i+1], p[i+1:]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, base) {
			continue
		}
		// Dot-directories are noise in a picker until the user is typing one.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if !entryIsDir(dir+name, e) {
			continue
		}
		out = append(out, dir+name)
	}
	return out
}

// entryIsDir reports whether the entry is a directory to walk into. A symlink is
// stat'ed rather than skipped: a link to a work tree is a location like any
// other, and a directory entry describes the link itself.
func entryIsDir(path string, e fs.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// trimTrailingSep drops a path's trailing separator, except from the root — it
// is nothing but its separator. A location holds its directory canonically (see
// core.LaunchTarget), so a query spelled with a trailing one has to be read back
// without it to reach the row. A path being typed is canonicalized outright
// (pathNeedle); this is what is left of that for a query that only looks a
// little like a path.
func trimTrailingSep(p string) string {
	if len(p) <= 1 {
		return p
	}
	return strings.TrimSuffix(p, "/")
}

// dirExists reports whether p names a directory that is there now.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
