package core

import (
	"os"
	"strings"
	"sync"
)

// HomeDir is the prefix ShortPath abbreviates to "~". It is read once per
// process: a running process' home does not move, and every list render asks
// for it. A home that cannot be determined is "", which abbreviates nothing.
var HomeDir = sync.OnceValue(func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
})

// ShortPath abbreviates the home prefix and, when still too long, keeps the tail
// — the end of a path is what distinguishes it.
func ShortPath(p string, w int) string {
	return TruncateLeftCells(AbbrevHome(p, HomeDir()), w)
}

// AbbrevHome writes the home prefix as "~", and only on a path-component
// boundary: a sibling home like /home/uX shares every letter of /home/u without
// being under it, and "~X/y" would name a directory that does not exist.
func AbbrevHome(p, home string) string {
	if home == "" {
		return p
	}
	after, ok := strings.CutPrefix(p, home)
	if !ok || (after != "" && after[0] != '/') {
		return p
	}
	return "~" + after
}
