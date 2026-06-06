package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// workdirHash returns the first 12 hex characters of sha256(canonicalWorkDir).
// Canonicalization is filepath.Clean(filepath.Abs(p)) — no symlink resolution.
func workdirHash(canonicalWorkDir string) string {
	sum := sha256.Sum256([]byte(canonicalWorkDir))
	return hex.EncodeToString(sum[:])[:12]
}

// escapeName replaces every '-' in s with '--', making the component
// distinguishable from the separator '-' in the generated name.
func escapeName(s string) string {
	return strings.ReplaceAll(s, "-", "--")
}

// GenerateName returns the deterministic cmdman command name for a compose command.
// Format: <workdir-hash>-<escaped-project>-<escaped-command>
// Every '-' in project or command is replaced by '--'; the workdir-hash is hex
// (never contains '-'), so the generated form is uniquely decomposable.
func GenerateName(wdHash, project, command string) string {
	return wdHash + "-" + escapeName(project) + "-" + escapeName(command)
}

// hashCanonical is the canonical struct used as hash input.
// Fields that affect runtime behavior are included; compose metadata (file path,
// project name, generated labels) are excluded so the hash reflects only the
// command's execution identity.
//
// Excluded fields:
//   - ComposeFile, Project (compose metadata, not runtime)
//   - GeneratedName (derived from WorkDir+Project+Command)
//   - reserved compose labels (added at plan time)
type hashCanonical struct {
	Name            string            `json:"name"`
	Args            []string          `json:"args"`
	Dir             string            `json:"dir"`
	Env             []string          `json:"env"` // sorted KEY=VALUE; env_file + env: merged
	RestartPolicy   string            `json:"restart_policy"`
	MaxRetries      int               `json:"max_retries,omitzero"`
	StopSignal      string            `json:"stop_signal"`
	Tty             bool              `json:"tty"`
	ScrollbackBytes int               `json:"scrollback_bytes"`
	LogDriver       string            `json:"log_driver"`
	LogOpts         map[string]string `json:"log_opts,omitzero"`
	UserLabels      map[string]string `json:"user_labels,omitzero"`
	After           []hashAfterSpec   `json:"after,omitzero"`
}

type hashAfterSpec struct {
	Name      string `json:"name"`
	Condition string `json:"condition"`
}

// Hash computes the config hash for a normalized command.
// Returns a string of the form "sha256:<64-hex-chars>".
// The hash is computed over a small canonical struct that excludes compose
// metadata (file path, project name) and generated labels.
func Hash(cmd Command) (string, error) {
	// Sort log opts keys for determinism.
	var logOpts map[string]string
	if len(cmd.LogOpts) > 0 {
		logOpts = cmd.LogOpts
	}

	// Sort user labels.
	var userLabels map[string]string
	if len(cmd.Labels) > 0 {
		userLabels = cmd.Labels
	}

	// Build sorted after list (already sorted during normalization, but be explicit).
	var afterList []hashAfterSpec
	if len(cmd.After) > 0 {
		afterList = make([]hashAfterSpec, len(cmd.After))
		for i, a := range cmd.After {
			afterList[i] = hashAfterSpec{Name: a.Name, Condition: string(a.Condition)}
		}
		slices.SortFunc(afterList, func(a, b hashAfterSpec) int {
			return strings.Compare(a.Name, b.Name)
		})
	}

	// Env is already sorted by mapToEnvSlice; copy to be safe.
	envCopy := make([]string, len(cmd.Env))
	copy(envCopy, cmd.Env)
	slices.Sort(envCopy)

	canon := hashCanonical{
		Name:            cmd.Name,
		Args:            cmd.Args,
		Dir:             cmd.Dir,
		Env:             envCopy,
		RestartPolicy:   string(cmd.RestartPolicy),
		StopSignal:      cmd.StopSignal,
		Tty:             cmd.Tty,
		ScrollbackBytes: cmd.ScrollbackBytes,
		LogDriver:       string(cmd.LogDriver),
		LogOpts:         sortedMapCopy(logOpts),
		UserLabels:      sortedMapCopy(userLabels),
		After:           afterList,
	}

	data, err := json.Marshal(canon)
	if err != nil {
		return "", fmt.Errorf("marshal hash input: %w", err)
	}

	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// sortedMapCopy returns a new map with the same contents as m (for deterministic
// JSON marshaling — Go's encoding/json already sorts map keys, but we make a
// copy to avoid aliasing).
func sortedMapCopy(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	maps.Copy(out, m)
	return out
}
