package mux

import (
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v4"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// Spec is the cmdman-facing layout spec — the body of the YAML "mux:" section.
// Distinct from [muxctl.MuxSpec]: leaves carry an unresolved [PaneSpec.Command]
// (cmdman command name/ID or compose service name) and a [PaneSpec.Mode],
// which [Build] turns into concrete argv.
type Spec struct {
	Driver    string            `yaml:"driver,omitempty"`
	DriverOpt map[string]string `yaml:"driver_opt,omitempty"`
	Layouts   []Layout          `yaml:"layouts"`
	// Unknown captures unrecognized top-level keys (per project convention:
	// surface, never silently drop).
	Unknown map[string]any `yaml:",inline"`
}

// Layout is one named, switchable layout.
type Layout struct {
	Name string   `yaml:"name"`
	Root PaneSpec `yaml:"root"`
	// Unknown captures unrecognized keys at the layout level.
	Unknown map[string]any `yaml:",inline"`
}

// PaneSpec mirrors [muxctl.PaneSpec] but for the cmdman layer: a leaf carries
// a Command identifier (name or ID) plus a Mode, instead of resolved argv.
//
// In YAML, a leaf may also be written as a bare scalar string — the shorthand
// `- api` is equivalent to `- {command: api}`. See [PaneSpec.UnmarshalYAML].
type PaneSpec struct {
	// Container fields
	Dir    Direction     `yaml:"dir,omitempty"`
	Splits []muxctl.Size `yaml:"splits,omitempty"`
	Panes  []PaneSpec    `yaml:"panes,omitempty"`

	// Leaf fields
	Command string `yaml:"command,omitempty"`
	// Scale pins which replica of a scaled command this leaf targets (1-based).
	// Zero (the default, absent) leaves the leaf unpinned: `cmdman mux` then
	// cycles it across the command's replicas on successive invocations. It is a
	// compose concept; for standalone `cmdman mux` a positive Scale resolves the
	// suffixed command name "<command>-<Scale>".
	Scale  int               `yaml:"scale,omitempty"`
	Mode   Mode              `yaml:"mode,omitempty"`
	CmdOpt map[string]string `yaml:"cmd_opt,omitempty"`
	Focus  bool              `yaml:"focus,omitempty"`
}

// Direction is the split direction. Same grammar as [muxctl.Direction]:
// "h" = panes side by side, "v" = panes stacked.
type Direction string

const (
	DirHorizontal Direction = "h"
	DirVertical   Direction = "v"
)

// Mode is the leaf's view mode. The default ("") is treated as [ModeAttach].
type Mode string

const (
	ModeAttach Mode = "attach"
	ModeLogs   Mode = "logs"
)

// UnmarshalYAML accepts either a bare-scalar shorthand (interpreted as the
// leaf's Command) or a mapping with the [PaneSpec] fields.
func (p *PaneSpec) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		p.Command = node.Value
		return nil
	}
	type rawPane PaneSpec
	var r rawPane
	if err := node.Decode(&r); err != nil {
		return err
	}
	*p = PaneSpec(r)
	return nil
}

// IsLeaf reports whether p is a leaf (has Command, no container fields).
func (p PaneSpec) IsLeaf() bool {
	return p.Command != "" && p.Dir == "" && len(p.Splits) == 0 && len(p.Panes) == 0
}

// IsContainer reports whether p is a container (Dir/Splits/Panes set, no
// Command).
func (p PaneSpec) IsContainer() bool {
	return p.Command == "" && (p.Dir != "" || len(p.Panes) > 0 || len(p.Splits) > 0)
}

// Decode parses a [Spec] from r. The reader must contain a single document
// with a top-level "mux:" key wrapping the spec body — the standalone
// `cmdman mux` file form.
//
// Decode does not validate the spec; [Build] surfaces validation errors via
// [muxctl.MuxSpec.Validate] after leaf resolution.
func Decode(r io.Reader) (Spec, error) {
	var wrapper struct {
		Mux     *Spec          `yaml:"mux"`
		Unknown map[string]any `yaml:",inline"`
	}
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&wrapper); err != nil {
		return Spec{}, fmt.Errorf("mux: decode: %w", err)
	}
	if wrapper.Mux == nil {
		return Spec{}, errors.New(`mux: missing top-level "mux:" key`)
	}
	return *wrapper.Mux, nil
}
