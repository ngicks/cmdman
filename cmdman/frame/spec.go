// Package frame provides parsing, validation, discovery, and layout carving for
// cmdman frame definitions — the user-level YAML files that dock display
// components (a switcher column, a status bar, an arbitrary command) around the
// screen edges, leaving the remaining rectangle to whatever the caller renders
// in it.
//
// A def is a flat, ordered array of entries. Order is the nesting: each entry
// carves its slice out of the rectangle left over by the entries before it, so
// [left 20%, bottom 2] gives a full-height side column with a bottom bar that
// spans only the remainder, while [bottom 2, left 20%] gives a full-width bottom
// bar with a shorter side column.
package frame

import (
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/pkg/muxctl"
	"go.yaml.in/yaml/v4"
)

// Edge is the screen edge an entry docks to.
type Edge string

const (
	EdgeTop    Edge = "top"
	EdgeBottom Edge = "bottom"
	EdgeLeft   Edge = "left"
	EdgeRight  Edge = "right"
)

// Built-in component names a def may reference (D16). cmdman ships no default
// frame; these are the components a def can name without declaring argv.
const (
	ComponentSwitcher  = "switcher"
	ComponentStatusbar = "statusbar"
)

var builtinComponents = []string{ComponentStatusbar, ComponentSwitcher}

// BuiltinComponents returns the built-in component names, sorted.
func BuiltinComponents() []string {
	return slices.Clone(builtinComponents)
}

// IsBuiltinComponent reports whether name is a built-in component.
func IsBuiltinComponent(name string) bool {
	return slices.Contains(builtinComponents, name)
}

// ---- Raw YAML structs -------------------------------------------------------

// RawSpec is the raw YAML model of a frame def file: a top-level "frame:" key
// wrapping the entry array. The wrapper (rather than a bare top-level sequence)
// leaves room for further def-level sections.
type RawSpec struct {
	Frame []RawEntry `yaml:"frame"`
	// Unknown captures unrecognized top-level keys so [Normalize] can warn.
	Unknown map[string]any `yaml:",inline"`
}

// RawEntry is the raw YAML shape of one frame entry. Exactly one of Component
// and Command must be set.
type RawEntry struct {
	Edge string `yaml:"edge"`
	Size Size   `yaml:"size"`
	// Component names a built-in cmdman widget (see [BuiltinComponents]). The
	// invocation it resolves to is supplied by the caller of [Spec.Carve].
	Component string `yaml:"component,omitempty"`
	// Command is argv, passed through verbatim.
	Command []string `yaml:"command,omitempty"`
	// Managed opts a Command entry into cmdman supervision so it survives the
	// frame; the default is an ephemeral pane (D19). It is meaningless on a
	// Component entry and rejected there.
	Managed bool `yaml:"managed,omitempty"`
	// Hooks overrides the config-level default_hooks for the supervised command
	// a Managed entry becomes (D17). Hooks are monitor behavior, so an entry
	// that is not managed has nothing to attach them to: [Normalize] warns and
	// ignores them there.
	Hooks model.HookSet `yaml:"hooks,omitempty"`
	// Unknown captures unrecognized entry keys so [Normalize] can warn.
	Unknown map[string]any `yaml:",inline"`
}

// Size is an entry's extent along the edge it docks to: character cells, or a
// percentage of the rectangle remaining at that entry's turn (D30). The zero
// value means absent.
type Size struct {
	// N is positive; percentages are in 1..100.
	N int
	// Percent selects percent-of-remainder over character cells.
	Percent bool
}

// MuxSize returns the [muxctl.Size] the carved pane tree uses. Cells map to
// muxctl's absolute form and percentages to its percent-of-parent form, which
// carving makes percent-of-remainder by construction.
func (s Size) MuxSize() muxctl.Size {
	return muxctl.Size{N: s.N, Absolute: !s.Percent, Percent: s.Percent}
}

// String returns the YAML scalar form: "N%" when Percent, "Nc" otherwise.
func (s Size) String() string {
	return s.MuxSize().String()
}

// UnmarshalYAML accepts a bare integer (`size: 2`) or a size string
// ("2", "2c", "20%"). A bare weight is read as character cells — unlike
// [muxctl.ParseSize], a frame entry has no siblings to share a ratio with.
func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	var cells int
	if err := node.Decode(&cells); err == nil {
		if cells <= 0 {
			return fmt.Errorf("%w: %d must be > 0", muxctl.ErrInvalidSize, cells)
		}
		*s = Size{N: cells}
		return nil
	}

	var str string
	if err := node.Decode(&str); err != nil {
		return fmt.Errorf("frame: size must be an integer or a size string: %w", err)
	}
	parsed, err := muxctl.ParseSize(str)
	if err != nil {
		return err
	}
	*s = Size{N: parsed.N, Percent: parsed.Percent}
	return nil
}

// MarshalYAML emits the scalar form returned by [Size.String].
func (s Size) MarshalYAML() (any, error) {
	return s.String(), nil
}

// ---- Normalized spec --------------------------------------------------------

// Spec is a validated frame def. Entries are in file order, which is the
// carving order (see the package doc).
type Spec struct {
	// Path is the file the def was loaded from, empty when it was decoded from
	// memory.
	Path string
	// Entries is non-empty.
	Entries []Entry
}

// Entry is a validated frame entry: a docked pane holding either a built-in
// Component or a Command argv, never both.
type Entry struct {
	Edge      Edge
	Size      muxctl.Size
	Component string
	Command   []string
	Managed   bool
	// Hooks is non-nil only on a Managed entry; see [RawEntry.Hooks].
	Hooks model.HookSet
}

// ---- Decoding ---------------------------------------------------------------

// Decode parses a [RawSpec] from r. The reader must contain a single YAML
// document with a top-level "frame:" key wrapping the entry array.
//
// Decode does not validate; call [Normalize] for that. Unknown keys are
// captured into the surrounding struct's Unknown field rather than dropped or
// hard-failed (per the project YAML convention).
func Decode(r io.Reader) (RawSpec, error) {
	var raw RawSpec
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return RawSpec{}, fmt.Errorf("frame: decode: %w", err)
	}
	return raw, nil
}

// DecodeFile reads and YAML-decodes the frame def at path.
func DecodeFile(path string) (RawSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		// Returned bare: *os.PathError already reads "open <path>: …", so any
		// prefix naming the same open doubles the path in the message.
		return RawSpec{}, err
	}
	defer f.Close()

	var raw RawSpec
	dec := yaml.NewDecoder(f)
	if err := dec.Decode(&raw); err != nil {
		return RawSpec{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return raw, nil
}
