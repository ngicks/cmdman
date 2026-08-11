package frame

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ngicks/go-common/contextkey"
)

// warnUnknownFields logs one warning per unrecognized YAML key in sorted order,
// so stray or misspelled fields surface to the user without failing the load.
// The logger comes from ctx (contextkey.ValueSlogLoggerDefault).
func warnUnknownFields(ctx context.Context, unknown map[string]any, msg string, args ...any) {
	logger := contextkey.ValueSlogLoggerDefault(ctx)
	for _, key := range slices.Sorted(maps.Keys(unknown)) {
		logger.WarnContext(ctx, msg, append(slices.Clone(args), "field", key)...)
	}
}

// Normalize validates raw and returns the frame def it describes. path is the
// file raw was decoded from; it is recorded on the result and used in error and
// warning messages, and may be empty for an in-memory def.
//
// Unrecognized keys are warned about, never dropped silently and never fatal.
// Everything else — an unknown edge, a missing size, a component that is not
// built in, an entry that sets both or neither of component/command — is an
// error: the def cannot be realized as written.
func Normalize(ctx context.Context, path string, raw RawSpec) (Spec, error) {
	warnUnknownFields(
		ctx,
		raw.Unknown,
		"frame: ignoring unrecognized top-level field",
		"path",
		path,
	)

	if len(raw.Frame) == 0 {
		return Spec{}, fmt.Errorf(
			"frame %s: no entries; the def needs a top-level frame: array", describePath(path),
		)
	}

	entries := make([]Entry, 0, len(raw.Frame))
	for i, re := range raw.Frame {
		warnUnknownFields(
			ctx,
			re.Unknown,
			"frame: ignoring unrecognized entry field",
			"path",
			path,
			"entry",
			i,
		)

		entry, err := normalizeEntry(re)
		if err != nil {
			return Spec{}, fmt.Errorf("frame %s: entry %d: %w", describePath(path), i, err)
		}
		entries = append(entries, entry)
	}

	return Spec{Path: path, Entries: entries}, nil
}

func normalizeEntry(re RawEntry) (Entry, error) {
	edge := Edge(re.Edge)
	switch edge {
	case EdgeTop, EdgeBottom, EdgeLeft, EdgeRight:
	case "":
		return Entry{}, fmt.Errorf("edge is required (one of %s)", strings.Join(edgeNames(), ", "))
	default:
		return Entry{}, fmt.Errorf(
			"unknown edge %q (allowed: %s)", re.Edge, strings.Join(edgeNames(), ", "),
		)
	}

	if re.Size.N == 0 {
		return Entry{}, errors.New("size is required (N cells or N%)")
	}

	hasComponent := re.Component != ""
	hasCommand := len(re.Command) > 0
	switch {
	case hasComponent && hasCommand:
		return Entry{}, errors.New("component: and command: are mutually exclusive")
	case !hasComponent && !hasCommand:
		return Entry{}, errors.New("one of component: or command: is required")
	case hasComponent:
		if !IsBuiltinComponent(re.Component) {
			return Entry{}, fmt.Errorf(
				"unknown component %q (built in: %s)",
				re.Component, strings.Join(BuiltinComponents(), ", "),
			)
		}
		if re.Managed {
			return Entry{}, errors.New("managed: applies to command: entries only")
		}
	}

	return Entry{
		Edge:      edge,
		Size:      re.Size.MuxSize(),
		Component: re.Component,
		Command:   slices.Clone(re.Command),
		Managed:   re.Managed,
	}, nil
}

func edgeNames() []string {
	return []string{string(EdgeTop), string(EdgeBottom), string(EdgeLeft), string(EdgeRight)}
}

func describePath(path string) string {
	if path == "" {
		return "def"
	}
	return path
}
