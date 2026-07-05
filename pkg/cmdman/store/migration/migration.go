// Package migration holds the ordered SQL schema migrations applied by the
// cmdman store on open. Each migration is an embedded ".sql" file named
// "NNNN_description.sql"; the numeric prefix is the schema version it brings
// the database to. The same files are the single source of truth for a fresh
// database (the whole chain is replayed) and for upgrading an existing one.
package migration

import (
	"cmp"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

//go:embed *.sql
var migrationFS embed.FS

// Migration is a single ordered schema migration: the version it brings the
// database to and the DDL that performs it.
type Migration struct {
	Version int
	SQL     string
}

var all = mustLoad()

// All returns the schema migrations ordered ascending by version.
func All() []Migration {
	return all
}

// MaxVersion returns the highest migration version in the chain, or 0 when the
// chain is empty.
func MaxVersion() int {
	if len(all) == 0 {
		return 0
	}
	return all[len(all)-1].Version
}

func mustLoad() []Migration {
	names, err := fs.Glob(migrationFS, "*.sql")
	if err != nil {
		panic(fmt.Sprintf("migrations: glob embedded files: %v", err))
	}
	migs := make([]Migration, 0, len(names))
	for _, name := range names {
		ver, err := parseVersion(name)
		if err != nil {
			panic(fmt.Sprintf("migrations: %v", err))
		}
		data, err := migrationFS.ReadFile(name)
		if err != nil {
			panic(fmt.Sprintf("migrations: read %s: %v", name, err))
		}
		migs = append(migs, Migration{Version: ver, SQL: string(data)})
	}
	slices.SortFunc(migs, func(a, b Migration) int {
		return cmp.Compare(a.Version, b.Version)
	})
	for i := 1; i < len(migs); i++ {
		if migs[i].Version == migs[i-1].Version {
			panic(fmt.Sprintf("migrations: duplicate version %d", migs[i].Version))
		}
	}
	return migs
}

func parseVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration file %q missing version prefix", name)
	}
	ver, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration file %q has invalid version prefix: %w", name, err)
	}
	if ver <= 0 {
		return 0, fmt.Errorf("migration file %q version must be positive", name)
	}
	return ver, nil
}
