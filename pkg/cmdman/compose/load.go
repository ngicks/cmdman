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
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/hrstr"
	"github.com/ngicks/go-common/contextkey"
	"go.yaml.in/yaml/v4"
)

// defaultFileNames are the candidate compose file names searched in CWD order.
var defaultFileNames = []string{"cmd-compose.yaml", "cmd-compose.yml"}

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
// When opts.File is non-empty it is used directly (resolved relative to cwd).
// Otherwise the two default file names are tried in cwd order.
// Returns the absolute compose file path and decoded spec.
func DiscoverFile(cwd string, opts NormalizeOpts) (path string, raw RawComposeSpec, err error) {
	if opts.File != "" {
		path = opts.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
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

	// effective work directory
	effectiveWorkDir := opts.WorkDir
	if effectiveWorkDir == "" {
		effectiveWorkDir = raw.WorkDir
	}
	if effectiveWorkDir == "" {
		effectiveWorkDir = cwd
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

		cmdDir := resolvePath(effectiveWorkDir, cmd.Dir)

		// commandEnv: KEY→VALUE for env_file + env: entries (not OS env).
		// finalEnv: OS + env_file + env: (for args interpolation).
		commandEnv, finalEnv, err := buildCommandEnv(effectiveWorkDir, cmd)
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

		resolvedLogOpts := resolveLogOptPaths(effectiveWorkDir, cmd.LogOpts)

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
) (commandEnv, finalEnv map[string]string, err error) {
	osEnv := osEnvMap()

	// Layer 2: env_files in order.
	envFileAccum := make(map[string]string)

	for i, ef := range cmd.EnvFile {
		required := true
		if ef.Required != nil {
			required = *ef.Required
		}
		path := resolvePath(workDir, ef.Path)

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

// resolvePath resolves p relative to workDir. Absolute paths are used as-is.
// Empty p returns workDir.
func resolvePath(workDir, p string) string {
	if p == "" {
		return workDir
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// resolveLogOptPaths resolves the "path" key in log opts against workDir.
func resolveLogOptPaths(workDir string, opts map[string]string) map[string]string {
	if len(opts) == 0 {
		return opts
	}
	out := make(map[string]string, len(opts))
	for k, v := range opts {
		if k == "path" {
			out[k] = resolvePath(workDir, v)
		} else {
			out[k] = v
		}
	}
	return out
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
		switch spec.Condition {
		case ConditionCompleted, ConditionStarted, ConditionCompletedSuccessfully:
		default:
			return nil, fmt.Errorf(
				"dependency %q has unknown condition %q"+
					" (allowed: completed, started, completed_successfully)",
				n,
				spec.Condition,
			)
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
