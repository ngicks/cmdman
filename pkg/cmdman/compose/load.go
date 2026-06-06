package compose

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/hrstr"
	"github.com/ngicks/go-common/contextkey"
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

// NormalizeOpts holds caller-supplied overrides for Normalize.
type NormalizeOpts struct {
	// File is an explicit compose file path. When empty, discovery is used.
	File string
	// ProjectName overrides the YAML top-level name:.
	ProjectName string
	// WorkDir overrides the YAML work_dir: and CWD fallback.
	WorkDir string
}

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

// warnUnknownFields logs one warning per unrecognized YAML key in sorted order,
// so stray or misspelled fields surface to the user without failing the load.
// The logger comes from ctx (contextkey.ValueSlogLoggerDefault), falling back to slog.Default().
func warnUnknownFields(ctx context.Context, unknown map[string]any, msg string, args ...any) {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	for _, key := range slices.Sorted(maps.Keys(unknown)) {
		logger.WarnContext(ctx, msg, append(slices.Clone(args), "field", key)...)
	}
}

// LoadAndNormalize discovers the compose file relative to the current working
// directory and normalizes it using opts. It is the shared entry point for
// operations that require a compose file (create, up).
func LoadAndNormalize(opts NormalizeOpts) (ComposeSpec, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ComposeSpec{}, fmt.Errorf("get working directory: %w", err)
	}
	filePath, raw, err := DiscoverFile(cwd, opts)
	if err != nil {
		return ComposeSpec{}, err
	}
	return Normalize(context.Background(), filePath, raw, opts)
}

// Normalize resolves, validates, and normalizes a RawComposeSpec.
// composeFilePath is the absolute path of the compose file; it is passed explicitly
// because the caller (DiscoverFile) already resolved it.
//
// Env layering (resolved-decision 9):
//  1. OS env (base, for interpolation context only — not stored in the slice).
//  2. env_file entries in list order; each file's interpolation sees OS + prior env_files.
//  3. env: entries in list order; interpolation sees OS + merged env_file results.
//  4. args: interpolation sees the final per-command env (OS + env_file + env:).
//
// The stored Env slice contains only entries that were explicitly set by env_file or env:
// (not raw OS env), so the hash is stable across OS environments.
func Normalize(
	ctx context.Context,
	composeFilePath string,
	raw RawComposeSpec,
	opts NormalizeOpts,
) (ComposeSpec, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return ComposeSpec{}, fmt.Errorf("get working directory: %w", err)
	}

	// effective compose file path
	if !filepath.IsAbs(composeFilePath) {
		composeFilePath = filepath.Join(cwd, composeFilePath)
	}
	composeFilePath = filepath.Clean(composeFilePath)

	// baseEnv is the OS environment plus compose path variables, used as the
	// base for every interpolation in this spec (work_dir, dirs, env files,
	// env:, args, log opts). The compose path variables are interpolation-only:
	// they never land in a command's stored Env unless an author copies them
	// through env:.
	baseEnv := osEnvMap()
	baseEnv[ENV_CMDMAN_COMPOSE_FILE] = composeFilePath
	baseEnv[ENV_CMDMAN_COMPOSE_DIR] = filepath.Dir(composeFilePath)
	baseLookup := buildLookup(baseEnv)

	// effective work directory
	effectiveWorkDir := opts.WorkDir
	if effectiveWorkDir == "" {
		effectiveWorkDir = raw.WorkDir
	}
	if effectiveWorkDir == "" {
		effectiveWorkDir = cwd
	}
	effectiveWorkDir, err = template.Substitute(effectiveWorkDir, baseLookup)
	if err != nil {
		return ComposeSpec{}, fmt.Errorf("work_dir interpolation: %w", err)
	}
	if !filepath.IsAbs(effectiveWorkDir) {
		effectiveWorkDir = filepath.Join(cwd, effectiveWorkDir)
	}
	// Canonicalize: Clean+Abs only; no EvalSymlinks (resolved-decision 22).
	effectiveWorkDir = filepath.Clean(effectiveWorkDir)

	// project name
	project := opts.ProjectName
	if project == "" {
		project = raw.Name
	}
	if project == "" {
		return ComposeSpec{}, errors.New(
			"project name is required: set name: in the compose file or pass --project-name",
		)
	}
	if err := validateName("project", project); err != nil {
		return ComposeSpec{}, err
	}

	warnUnknownFields(
		ctx,
		raw.Unknown,
		"compose: ignoring unrecognized top-level field",
		"project",
		project,
	)

	wdHash := workdirHash(effectiveWorkDir)

	// sort command names for deterministic ordering
	cmdNames := make([]string, 0, len(raw.Commands))
	for n := range raw.Commands {
		cmdNames = append(cmdNames, n)
	}
	slices.Sort(cmdNames)

	normalized := make([]Command, 0, len(raw.Commands))

	for _, name := range cmdNames {
		cmd := raw.Commands[name]
		if err := validateName("command", name); err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: %w", name, err)
		}

		warnUnknownFields(
			ctx,
			cmd.Unknown,
			"compose: ignoring unrecognized command field",
			"project",
			project,
			"command",
			name,
		)

		dirVal, err := template.Substitute(cmd.Dir, baseLookup)
		if err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: dir interpolation: %w", name, err)
		}
		cmdDir := resolvePath(effectiveWorkDir, dirVal)

		// commandEnv: KEY→VALUE for env_file + env: entries (not OS env).
		// finalEnv: OS + env_file + env: (for args interpolation).
		commandEnv, finalEnv, err := buildCommandEnv(effectiveWorkDir, cmd, baseEnv)
		if err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: %w", name, err)
		}

		finalLookup := buildLookup(finalEnv)

		interpolatedArgs := make([]string, len(cmd.Args))
		for i, arg := range cmd.Args {
			v, interpErr := template.Substitute(arg, finalLookup)
			if interpErr != nil {
				return ComposeSpec{}, fmt.Errorf(
					"command %q: args[%d] interpolation: %w",
					name,
					i,
					interpErr,
				)
			}
			interpolatedArgs[i] = v
		}

		if err := validateUserLabels(cmd.Labels); err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: labels: %w", name, err)
		}

		resolvedLogOpts, err := resolveLogOptPaths(effectiveWorkDir, cmd.LogOpts, baseLookup)
		if err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: log_opts: %w", name, err)
		}

		afterList, err := normalizeAfter(name, cmd.After)
		if err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: after: %w", name, err)
		}

		envSlice := mapToEnvSlice(commandEnv)
		genName := GenerateName(wdHash, project, name)

		var (
			restartPolicy model.RestartPolicy
			maxRetries    int
		)
		if cmd.RestartPolicy != "" {
			restartPolicy, maxRetries, err = model.ParseRestartPolicy(cmd.RestartPolicy)
			if err != nil {
				return ComposeSpec{}, fmt.Errorf("command %q: %w", name, err)
			}
		}

		nc := Command{
			Name:            name,
			Dir:             cmdDir,
			Args:            interpolatedArgs,
			Env:             envSlice,
			Labels:          cmd.Labels,
			RestartPolicy:   restartPolicy,
			MaxRetries:      maxRetries,
			StopSignal:      cmd.StopSignal,
			Tty:             cmd.Tty,
			ScrollbackBytes: cmd.ScrollbackBytes,
			LogDriver:       logdriver.LogDriver(cmd.LogDriver),
			LogOpts:         resolvedLogOpts,
			After:           afterList,
			GeneratedName:   genName,
		}
		if err := validateRuntimeFields(nc); err != nil {
			return ComposeSpec{}, fmt.Errorf("command %q: %w", name, err)
		}
		normalized = append(normalized, nc)
	}

	if err := ValidateDAG(normalized); err != nil {
		return ComposeSpec{}, err
	}

	return ComposeSpec{
		ComposeFile: composeFilePath,
		Project:     project,
		WorkDir:     effectiveWorkDir,
		Commands:    normalized,
		Mux:         raw.Mux,
	}, nil
}

// buildCommandEnv processes env_file and env: for a single command.
// Returns:
//   - commandEnv: only the keys set by env_file + env: (stored & hashed).
//   - finalEnv: OS + commandEnv (used for args interpolation, not stored).
func buildCommandEnv(
	workDir string,
	cmd RawCommand,
	osEnv map[string]string,
) (commandEnv, finalEnv map[string]string, err error) {
	// Layer 2: env_files in order.
	envFileAccum := make(map[string]string)

	for i, ef := range cmd.EnvFile {
		required := true
		if ef.Required != nil {
			required = *ef.Required
		}
		// env_file paths interpolate against OS env + compose path variables +
		// prior env_file keys, so a project can point at a dot env file living
		// next to its compose file.
		interpPath, interpErr := template.Substitute(
			ef.Path, buildLookupFromMaps(osEnv, envFileAccum))
		if interpErr != nil {
			return nil, nil, fmt.Errorf("env_file[%d] interpolation: %w", i, interpErr)
		}
		path := resolvePath(workDir, interpPath)

		if !required {
			if _, statErr := os.Stat(path); statErr != nil {
				if os.IsNotExist(statErr) {
					continue
				}
				return nil, nil, fmt.Errorf("env_file[%d] stat %s: %w", i, path, statErr)
			}
		}

		f, openErr := os.Open(path)
		if openErr != nil {
			return nil, nil, fmt.Errorf("env_file[%d] open %s: %w", i, path, openErr)
		}

		// Each file's lookup sees OS env + previously accumulated env_file keys.
		lookupForFile := buildLookupFromMaps(osEnv, envFileAccum)
		fileVars, parseErr := dotenv.ParseWithLookup(f, lookupForFile)
		f.Close()
		if parseErr != nil {
			return nil, nil, fmt.Errorf("env_file[%d] parse %s: %w", i, path, parseErr)
		}
		maps.Copy(envFileAccum, fileVars)
	}

	// envFileMerged = OS + env_file (for env: interpolation lookup).
	envFileMerged := make(map[string]string, len(osEnv)+len(envFileAccum))
	maps.Copy(envFileMerged, osEnv)
	maps.Copy(envFileMerged, envFileAccum)
	envFileLookup := buildLookup(envFileMerged)

	// commandEnv starts with env_file results; env: overrides.
	cmdEnv := make(map[string]string, len(envFileAccum))
	maps.Copy(cmdEnv, envFileAccum)

	// Layer 3: env: entries.
	for j, entry := range cmd.Env {
		k, v, hasVal := strings.Cut(entry, "=")
		if !hasVal {
			// Bare key: inherit from envFileMerged (OS or env_file).
			if val, found := envFileMerged[k]; found {
				cmdEnv[k] = val
			}
			// If not present, skip (value is undefined at normalization time).
			continue
		}
		interpolated, interpErr := template.Substitute(v, envFileLookup)
		if interpErr != nil {
			return nil, nil, fmt.Errorf("env[%d] interpolation: %w", j, interpErr)
		}
		cmdEnv[k] = interpolated
	}

	// finalEnv = OS + commandEnv (for args interpolation).
	fin := make(map[string]string, len(osEnv)+len(cmdEnv))
	maps.Copy(fin, osEnv)
	maps.Copy(fin, cmdEnv)

	return cmdEnv, fin, nil
}

// osEnvMap converts os.Environ() into a map.
func osEnvMap() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		m[k] = v
	}
	return m
}

// buildLookup creates a template.Mapping from a flat env map.
func buildLookup(env map[string]string) template.Mapping {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// buildLookupFromMaps creates a template.Mapping that checks overlay first, then base.
func buildLookupFromMaps(base, overlay map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if v, ok := overlay[key]; ok {
			return v, true
		}
		if v, ok := base[key]; ok {
			return v, true
		}
		return "", false
	}
}

// mapToEnvSlice converts a map to sorted KEY=VALUE strings.
func mapToEnvSlice(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// resolvePath resolves p relative to workDir. Absolute paths are cleaned (so
// used as-is. Empty p returns workDir.
func resolvePath(workDir, p string) string {
	if p == "" {
		return workDir
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(workDir, p)
}

// resolveLogOptPaths interpolates and resolves the "path" key in log opts
// against workDir. Non-path opts are passed through unchanged.
func resolveLogOptPaths(
	workDir string,
	opts map[string]string,
	lookup template.Mapping,
) (map[string]string, error) {
	if len(opts) == 0 {
		return opts, nil
	}
	out := make(map[string]string, len(opts))
	for k, v := range opts {
		if k == "path" {
			interp, err := template.Substitute(v, lookup)
			if err != nil {
				return nil, fmt.Errorf("path interpolation: %w", err)
			}
			out[k] = resolvePath(workDir, interp)
		} else {
			out[k] = v
		}
	}
	return out, nil
}

// validateUserLabels rejects labels with the reserved cmdman.compose. prefix.
func validateUserLabels(labels map[string]string) error {
	for k := range labels {
		if strings.HasPrefix(k, LabelPrefix) {
			return fmt.Errorf("label key %q uses reserved prefix %q", k, LabelPrefix)
		}
	}
	return nil
}

// validateName enforces the naming rules for project and command names.
func validateName(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name must not be empty", kind)
	}
	if len(name) > 63 {
		return fmt.Errorf("%s name %q exceeds maximum length of 63 characters", kind, name)
	}
	if name[0] == '.' || name[0] == '-' {
		return fmt.Errorf("%s name %q must not start with '.' or '-'", kind, name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return fmt.Errorf("%s name %q must not contain whitespace", kind, name)
		}
		if r == '/' || r == '\\' {
			return fmt.Errorf("%s name %q must not contain path separators", kind, name)
		}
		if !isNameChar(r) {
			return fmt.Errorf(
				"%s name %q contains invalid character %q (allowed: [A-Za-z0-9._-])",
				kind,
				name,
				r,
			)
		}
	}
	return nil
}

func isNameChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '.' || r == '_' || r == '-'
}

// normalizeAfter converts the raw After map into a stable slice.
// Name is filled from the map key; Condition defaults to "completed".
func normalizeAfter(cmdName string, after map[string]AfterSpec) ([]AfterSpec, error) {
	if len(after) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(after))
	for n := range after {
		names = append(names, n)
	}
	slices.Sort(names)

	result := make([]AfterSpec, 0, len(after))
	for _, n := range names {
		if n == cmdName {
			return nil, fmt.Errorf("command cannot depend on itself")
		}
		spec := after[n]
		spec.Name = n
		if spec.Condition == "" {
			spec.Condition = ConditionCompleted
		}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		result = append(result, spec)
	}
	return result, nil
}

// validateRuntimeFields rejects compose-author mistakes that would otherwise
// only surface inside Service.Create. Fields left at their zero value are
// passed through because the service applies CmdmanConfig defaults later;
// only explicitly set values are checked here.
func validateRuntimeFields(nc Command) error {
	if len(nc.Args) == 0 {
		return errors.New("args is empty")
	}
	if nc.RestartPolicy != "" && !model.IsRestartPolicy(string(nc.RestartPolicy)) {
		return fmt.Errorf("invalid restart_policy %q", nc.RestartPolicy)
	}
	if nc.StopSignal != "" {
		if _, _, err := hrstr.ParseSignal(nc.StopSignal); err != nil {
			return fmt.Errorf("invalid stop_signal %q: %w", nc.StopSignal, err)
		}
	}
	if nc.ScrollbackBytes < 0 {
		return fmt.Errorf("scrollback_bytes must be non-negative: %d", nc.ScrollbackBytes)
	}
	if nc.LogDriver != "" && !model.IsLogDriver(string(nc.LogDriver)) {
		return fmt.Errorf("invalid log_driver %q", nc.LogDriver)
	}
	if nc.LogDriver != "" {
		for k, v := range nc.LogOpts {
			if err := logdriver.ValidateOpt(string(nc.LogDriver), k, v); err != nil {
				return err
			}
		}
	}
	return nil
}
