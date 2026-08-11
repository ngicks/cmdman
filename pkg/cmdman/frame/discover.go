package frame

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/pkg/cmdman/config"
)

// DiscoverFile resolves a frame def reference against cwd and the default frame
// dir (see [config.FrameConfigDir]), in the same order compose resolves its own
// defs:
//
//  1. as a filesystem path relative to cwd (a regular file there wins);
//  2. as a bare def name under the default frame dir
//     (see resolveNamedFrameFile);
//  3. otherwise the path form is decoded directly so the error message points
//     at the path the caller actually typed.
//
// Unlike compose there is no discovery from cwd alone: a frame def is always
// named explicitly, by config or by flag (D15).
func DiscoverFile(cwd, name string) (path string, raw RawSpec, err error) {
	if name == "" {
		return "", RawSpec{}, errors.New("frame: def name is required")
	}

	path = name
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
		raw, err = DecodeFile(path)
		return path, raw, err
	}

	named, ok, nerr := resolveNamedFrameFile(name)
	if nerr != nil {
		return "", RawSpec{}, nerr
	}
	if ok {
		raw, err = DecodeFile(named)
		return named, raw, err
	}

	raw, err = DecodeFile(path)
	return path, raw, err
}

// resolveNamedFrameFile resolves a bare def name to a file under the default
// frame dir (config.FrameConfigDir). The name maps to, in order:
//
//	<dir>/<name>          (when <name> already ends in .yaml/.yml)
//	<dir>/<name>.yaml
//	<dir>/<name>.yml
//
// ok is false when name is not a bare name (contains a path separator) or no
// matching entry exists. There is no directory form: a frame def is one file.
func resolveNamedFrameFile(name string) (path string, ok bool, err error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", false, nil
	}

	dir, err := config.FrameConfigDir()
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
	return "", false, nil
}

// ListNamedDefs returns the names of frame defs discoverable under the default
// frame dir (config.FrameConfigDir): one per <name>.yaml / <name>.yml file. The
// result is sorted and deduplicated. It returns nil (no error) when the
// directory does not exist or cannot be determined.
func ListNamedDefs() ([]string, error) {
	dir, err := config.FrameConfigDir()
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
		if e.IsDir() {
			continue
		}
		name := e.Name()
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

// LoadAndNormalize discovers the frame def named by name relative to the
// current working directory and normalizes it. It is the entry point for the
// frame verbs, which take the def name from config or a flag.
func LoadAndNormalize(ctx context.Context, name string) (Spec, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Spec{}, fmt.Errorf("get working directory: %w", err)
	}
	path, raw, err := DiscoverFile(cwd, name)
	if err != nil {
		return Spec{}, err
	}
	return Normalize(ctx, path, raw)
}
