package muxctl

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// MuxSpec is a driver-agnostic set of switchable layouts. Leaves contain
// resolved argv; higher layers translate their own user-facing specs.
type MuxSpec struct {
	// Driver selects and configures the backend multiplexer server.
	Driver DriverSpec `yaml:"driver,omitempty"`

	// Layouts must have unique names.
	Layouts []Layout `yaml:"layouts"`

	// Unknown captures unrecognized top-level keys for caller warnings.
	Unknown map[string]any `yaml:",inline"`
}

// DriverSpec is the YAML-facing description of which multiplexer server to bind
// to. Name selects the driver via [LookupDriver]; the remaining fields map onto
// a [ServerConfig] through [DriverSpec.ServerConfig].
type DriverSpec struct {
	// Name is the registered driver name. Empty lets the caller autodetect it.
	Name string `yaml:"name,omitempty"`

	// Path is the multiplexer executable. Empty selects the driver default.
	Path string `yaml:"path,omitempty"`

	// Socket selects the server instance: a bare name or a socket file path,
	// resolved driver-side (see [ServerConfig.Socket]). Empty means the default
	// server.
	Socket string `yaml:"socket,omitempty"`

	// Opts carries driver-specific keys only.
	Opts map[string]string `yaml:"opts,omitempty"`

	// Unknown captures unrecognized keys at the driver level; see
	// [MuxSpec.Unknown].
	Unknown map[string]any `yaml:",inline"`
}

// ServerConfig maps the YAML-facing spec onto the [ServerConfig] a driver
// consumes: Path→Executable, Socket→Socket, Opts→DriverOpt. Name is omitted —
// it selects the driver, not the server the chosen driver binds to.
func (d DriverSpec) ServerConfig() ServerConfig {
	return ServerConfig{
		Executable: d.Path,
		Socket:     d.Socket,
		DriverOpt:  d.Opts,
	}
}

// Layout is a named pane tree.
type Layout struct {
	// Name is required and unique within MuxSpec.Layouts.
	Name string `yaml:"name"`

	// Root is either a leaf or container.
	Root PaneSpec `yaml:"root"`

	// Unknown captures unrecognized keys at the layout level; see
	// [MuxSpec.Unknown].
	Unknown map[string]any `yaml:",inline"`
}

// Container holds a [PaneSpec] container's fields. All-zero means absent.
type Container struct {
	// Dir is the split direction: "h" (panes side by side) or "v" (stacked).
	// Single-letter tmux convention. Required for a container; must be empty
	// for a leaf.
	//
	// Note: tmux and zellij use inverted vocabulary — tmux -h ("horizontal
	// split") means panes side by side, which zellij calls "vertical". muxctl
	// follows tmux since tmux is the primary driver.
	Dir Direction `yaml:"dir,omitempty"`

	// Splits sizes the child panes. Index-parallel to Panes; absolute "Nc"
	// sizes are reserved first, and bare-weight sizes share the leftover by
	// ratio.
	Splits []Size `yaml:"splits,omitempty"`

	Panes []PaneSpec `yaml:"panes,omitempty"`
}

// Leaf holds a [PaneSpec] leaf's fields. All-zero means absent.
type Leaf struct {
	// Name is the pane name. For leaves it is required and serves as the map
	// key under which the runtime Pane is returned by [Session.ApplyLayout];
	// it must be unique within a layout. Containers leave Name empty.
	Name string `yaml:"name,omitempty"`

	// Cmd is the argv to spawn in the pane. Required for a leaf, empty for a
	// container. Drivers spawn this directly (no shell wrapping) so quoting
	// and rc-file behavior are not concerns of muxctl.
	Cmd []string `yaml:"cmd,omitempty"`

	// CmdOpt carries per-pane driver options.
	CmdOpt map[string]string `yaml:"cmd_opt,omitempty"`

	// Focus, when true, requests this leaf as the initial focus for its
	// layout. At most one focus per layout; the first leaf in document order
	// is used otherwise. It is honored for a layout applied by
	// [Session.ApplyLayout] only: a leaf of a frame tree becomes a frame pane,
	// and a frame pane is never a focus candidate.
	Focus bool `yaml:"focus,omitempty"`

	// CycleKey is the command name this leaf tracks for replica cycling
	// ("cycle-scale target"). When non-empty, the tmux driver stamps the
	// per-pane user option @cmdman_leaf with this value so cycle-scale can
	// locate the pane by command name. Empty means the leaf is not a
	// cycle-scale target (pinned or non-cycling).
	CycleKey string `yaml:"cycle_key,omitempty"`
}

// PaneSpec is a [Container] XOR a [Leaf], enforced by [MuxSpec.Validate].
type PaneSpec struct {
	Container `yaml:",inline"`
	Leaf      `yaml:",inline"`

	// Unknown captures unrecognized keys at the pane level; see
	// [MuxSpec.Unknown].
	Unknown map[string]any `yaml:",inline"`
}

// Direction is the split direction of a container [PaneSpec].
type Direction string

const (
	DirHorizontal Direction = "h" // panes side by side
	DirVertical   Direction = "v" // panes stacked
)

// IsLeaf reports whether p is a well-formed leaf node (Cmd set, no container
// fields). A pane that has both leaf and container fields, or neither, is
// reported as neither leaf nor container; [MuxSpec.Validate] catches it.
func (p PaneSpec) IsLeaf() bool {
	return len(p.Cmd) > 0 && p.Dir == "" && len(p.Splits) == 0 && len(p.Panes) == 0
}

// IsContainer reports whether p is a well-formed container node (any of
// Dir/Splits/Panes set, no Cmd). Leaves and empty panes return false.
func (p PaneSpec) IsContainer() bool {
	return len(p.Cmd) == 0 && (p.Dir != "" || len(p.Panes) > 0 || len(p.Splits) > 0)
}

// Size encodes a value from the YAML splits array: absolute character cells
// ("Nc"), a percent of the parent dimension ("N%", 1..100), or a proportional
// weight ("N"). N is always > 0. At most one of Absolute/Percent is true;
// both false means a bare weight.
type Size struct {
	// N is positive; percentages are in 1..100.
	N int
	// Absolute denotes character cells ("Nc").
	Absolute bool
	// Percent denotes a percentage of the parent dimension ("N%").
	Percent bool
}

// String returns the YAML scalar form: "Nc" when Absolute, "N%" when Percent,
// "N" otherwise.
func (s Size) String() string {
	switch {
	case s.Absolute:
		return strconv.Itoa(s.N) + "c"
	case s.Percent:
		return strconv.Itoa(s.N) + "%"
	default:
		return strconv.Itoa(s.N)
	}
}

// UnmarshalYAML decodes a YAML scalar into Size. See [ParseSize] for the
// grammar.
func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	var str string
	if err := node.Decode(&str); err != nil {
		return fmt.Errorf("muxctl: size must be a scalar string: %w", err)
	}
	parsed, err := ParseSize(str)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// MarshalYAML emits the scalar form returned by [Size.String].
func (s Size) MarshalYAML() (any, error) {
	return s.String(), nil
}

// ParseSize parses a size scalar:
//
//   - "Nc" → absolute, N character cells (Absolute=true).
//   - "N%" → percent of parent dimension (Percent=true). N must be in 1..100.
//   - "N"  → bare proportional weight (Absolute=false, Percent=false).
//   - N must be a positive integer (> 0).
func ParseSize(s string) (Size, error) {
	if s == "" {
		return Size{}, fmt.Errorf("%w: empty", ErrInvalidSize)
	}
	body := s
	absolute := false
	percent := false
	switch {
	case strings.HasSuffix(s, "c"):
		absolute = true
		body = strings.TrimSuffix(s, "c")
	case strings.HasSuffix(s, "%"):
		percent = true
		body = strings.TrimSuffix(s, "%")
	}
	n, err := strconv.Atoi(body)
	if err != nil {
		return Size{}, fmt.Errorf("%w: %q is not a number", ErrInvalidSize, s)
	}
	if n <= 0 {
		return Size{}, fmt.Errorf("%w: %q must be > 0", ErrInvalidSize, s)
	}
	if percent && n > 100 {
		return Size{}, fmt.Errorf("%w: %q percent must be in 1..100", ErrInvalidSize, s)
	}
	return Size{N: n, Absolute: absolute, Percent: percent}, nil
}

// Decode parses a [MuxSpec] from r. The reader must contain a single YAML
// document whose top-level mapping matches MuxSpec's shape (no outer
// wrapper). Callers reading from a file with an outer key (e.g. standalone
// "mux: ..." or a compose file's "mux:" section) extract that section first
// and pass the inner mapping to Decode.
//
// Decode does not validate the spec; call [MuxSpec.Validate] separately.
//
// Unknown keys at any decoded mapping level are captured into the surrounding
// struct's Unknown field rather than silently dropped or hard-failed (per the
// project YAML convention). Callers can iterate these in sorted order to warn.
func Decode(r io.Reader) (MuxSpec, error) {
	dec := yaml.NewDecoder(r)
	var spec MuxSpec
	if err := dec.Decode(&spec); err != nil {
		return MuxSpec{}, fmt.Errorf("muxctl: decode: %w", err)
	}
	return spec, nil
}
