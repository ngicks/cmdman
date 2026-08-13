package frame

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ngicks/cmdman/cmdman/model"
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
// Unrecognized keys — and a hooks: override on an entry that has no supervised
// command to apply it to — are warned about, never dropped silently and never
// fatal. Everything else — an unknown edge, a missing size, a component that is not
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

		entry, err := normalizeEntry(ctx, path, i, re)
		if err != nil {
			return Spec{}, fmt.Errorf("frame %s: entry %d: %w", describePath(path), i, err)
		}
		entries = append(entries, entry)
	}

	return Spec{Path: path, Entries: entries}, nil
}

func normalizeEntry(ctx context.Context, path string, i int, re RawEntry) (Entry, error) {
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

	hooks, err := normalizeHooks(ctx, path, i, re)
	if err != nil {
		return Entry{}, err
	}

	return Entry{
		Edge:      edge,
		Size:      re.Size.MuxSize(),
		Component: re.Component,
		Command:   slices.Clone(re.Command),
		Managed:   re.Managed,
		Hooks:     hooks,
	}, nil
}

// normalizeHooks keeps an entry's hook override only where it can take effect
// (D17): a managed entry carries it into its supervised command, anything else
// drops it with a warning. A kept set is validated here rather than at the
// far-away create path, so a typo names the def file it came from.
func normalizeHooks(
	ctx context.Context,
	path string,
	i int,
	re RawEntry,
) (model.HookSet, error) {
	if len(re.Hooks) == 0 {
		return nil, nil
	}
	if !re.Managed {
		contextkey.ValueSlogLoggerDefault(ctx).WarnContext(
			ctx,
			"frame: ignoring hooks: on an entry without managed: true",
			"path", path,
			"entry", i,
		)
		return nil, nil
	}
	if err := re.Hooks.Validate(); err != nil {
		return nil, err
	}
	return maps.Clone(re.Hooks), nil
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
