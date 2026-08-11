package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman"
	"go.yaml.in/yaml/v4"
)

// defaultFileNames are the candidate compose file names searched in CWD order.
var defaultFileNames = []string{"cmd-compose.yaml", "cmd-compose.yml"}

// namedComposeFile is the file searched inside a directory-style named compose
// project under the default compose dir (see cmdman.ComposeConfigDir).
const namedComposeFile = "compose.yaml"

// ENV_CMDMAN_COMPOSE_FILE is the interpolation variable exposing the absolute
// path of the compose file being loaded. Compose authors reference it to locate
// resources (env files, configs) next to the compose file, e.g.
// `env_file: ["${CMDMAN_COMPOSE_DIR}/app.env"]`.
const ENV_CMDMAN_COMPOSE_FILE = "CMDMAN_COMPOSE_FILE"

// ENV_CMDMAN_COMPOSE_DIR is the interpolation variable exposing the absolute
// directory path containing the compose file being loaded.
const ENV_CMDMAN_COMPOSE_DIR = "CMDMAN_COMPOSE_DIR"

// DiscoverFile discovers the compose file given opts and cwd.
//
// When opts.File is non-empty it is resolved in order:
//  1. as a filesystem path relative to cwd (a regular file there wins);
//  2. as a bare project name under the default compose dir
//     (see resolveNamedComposeFile);
//  3. otherwise the path form is decoded directly so the error message points
//     at the path the caller actually typed.
//
// When opts.File is empty the two default file names are tried in cwd order.
// Returns the absolute compose file path and decoded spec.
func DiscoverFile(cwd string, opts NormalizeOpts) (path string, raw RawComposeSpec, err error) {
	if opts.File != "" {
		path = opts.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			raw, err = decodeFile(path)
			return path, raw, err
		}

		named, ok, nerr := resolveNamedComposeFile(opts.File)
		if nerr != nil {
			return "", RawComposeSpec{}, nerr
		}
		if ok {
			raw, err = decodeFile(named)
			return named, raw, err
		}

		raw, err = decodeFile(path)
		return path, raw, err
	}

	var tried []string
	for _, name := range defaultFileNames {
		candidate := filepath.Join(cwd, name)
		tried = append(tried, candidate)
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return "", RawComposeSpec{}, fmt.Errorf("stat %s: %w", candidate, statErr)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err = decodeFile(candidate)
		return candidate, raw, err
	}
	return "", RawComposeSpec{}, fmt.Errorf(
		"no compose file found (tried %s); pass --file to specify one",
		strings.Join(tried, ", "),
	)
}

// resolveNamedComposeFile resolves a bare project name to a compose file under
// the default compose dir (cmdman.ComposeConfigDir). The name maps to, in order:
//
//	<dir>/<name>          (when <name> already ends in .yaml/.yml)
//	<dir>/<name>.yaml
//	<dir>/<name>.yml
//	<dir>/<name>/compose.yaml   (directory-style project)
//
// ok is false when name is not a bare name (contains a path separator) or no
// matching entry exists. A directory whose compose.yaml is missing is an error,
// since the directory clearly names a project the caller expected to find.
func resolveNamedComposeFile(name string) (path string, ok bool, err error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", false, nil
	}

	dir, err := cmdman.ComposeConfigDir()
	if err != nil {
		return "", false, err
	}
	if dir == "" {
		return "", false, nil
	}

	var candidates []string
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	candidates = append(candidates,
		filepath.Join(dir, name+".yaml"),
		filepath.Join(dir, name+".yml"),
	)
	for _, cand := range candidates {
		if info, statErr := os.Stat(cand); statErr == nil && info.Mode().IsRegular() {
			return cand, true, nil
		}
	}

	dirCand := filepath.Join(dir, name)
	if info, statErr := os.Stat(dirCand); statErr == nil && info.IsDir() {
		cand := filepath.Join(dirCand, namedComposeFile)
		if info2, statErr2 := os.Stat(cand); statErr2 == nil && info2.Mode().IsRegular() {
			return cand, true, nil
		}
		return "", false, fmt.Errorf(
			"compose project %q: %s has no %s", name, dirCand, namedComposeFile)
	}

	return "", false, nil
}

// ListNamedProjects returns the names of compose projects discoverable under
// the default compose dir (cmdman.ComposeConfigDir): one per <name>.yaml /
// <name>.yml file and one per directory containing a compose.yaml. The result
// is sorted and deduplicated. It returns nil (no error) when the directory does
// not exist or cannot be determined.
func ListNamedProjects() ([]string, error) {
	dir, err := cmdman.ComposeConfigDir()
	if err != nil || dir == "" {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	seen := make(map[string]struct{})
	var names []string
	add := func(n string) {
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			cand := filepath.Join(dir, name, namedComposeFile)
			if info, statErr := os.Stat(cand); statErr == nil && info.Mode().IsRegular() {
				add(name)
			}
			continue
		}
		switch {
		case strings.HasSuffix(name, ".yaml"):
			add(strings.TrimSuffix(name, ".yaml"))
		case strings.HasSuffix(name, ".yml"):
			add(strings.TrimSuffix(name, ".yml"))
		}
	}
	slices.Sort(names)
	return names, nil
}

// MuxProject is a named compose project under the default compose dir that
// declares a top-level mux: section, paired with its loaded spec.
type MuxProject struct {
	Name string
	Spec ComposeSpec
}

// ListMuxProjects loads the named projects under the default compose dir
// (see [ListNamedProjects]) and returns those whose compose file declares a
// mux: section. Projects that fail to load/normalize are skipped, so a single
// broken file does not hide the others. The result preserves the sorted name
// order of [ListNamedProjects].
func ListMuxProjects() ([]MuxProject, error) {
	names, err := ListNamedProjects()
	if err != nil {
		return nil, err
	}
	var out []MuxProject
	for _, name := range names {
		spec, err := LoadAndNormalize(NormalizeOpts{File: name})
		if err != nil {
			continue
		}
		if spec.Mux != nil {
			out = append(out, MuxProject{Name: name, Spec: spec})
		}
	}
	return out, nil
}

// DecodeFile reads and YAML-decodes a compose file at path.
func DecodeFile(path string) (RawComposeSpec, error) {
	return decodeFile(path)
}

func decodeFile(path string) (RawComposeSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return RawComposeSpec{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var spec RawComposeSpec
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&spec); err != nil {
		return RawComposeSpec{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return spec, nil
}
