package frame_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/ngicks/go-common/contextkey"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/pkg/cmdman/frame"
	"github.com/ngicks/cmdman/pkg/muxctl"
)

// normalize decodes content and normalizes it with a discarding logger.
func normalize(t *testing.T, content string) (frame.Spec, error) {
	t.Helper()
	raw, err := frame.Decode(strings.NewReader(content))
	assert.NilError(t, err)
	return frame.Normalize(context.Background(), "", raw)
}

func TestNormalize(t *testing.T) {
	spec, err := normalize(t, `
frame:
  - edge: left
    size: 20%
    component: switcher
  - edge: bottom
    size: 2
    component: statusbar
  - edge: right
    size: 30%
    command: ["btop"]
    managed: true
`)
	assert.NilError(t, err)
	assert.Equal(t, len(spec.Entries), 3)

	assert.Equal(t, spec.Entries[0].Edge, frame.EdgeLeft)
	assert.Equal(t, spec.Entries[0].Size, muxctl.Size{N: 20, Percent: true})
	assert.Equal(t, spec.Entries[0].Component, frame.ComponentSwitcher)
	assert.Assert(t, spec.Entries[0].Command == nil)

	assert.Equal(t, spec.Entries[1].Edge, frame.EdgeBottom)
	assert.Equal(t, spec.Entries[1].Size, muxctl.Size{N: 2, Absolute: true})

	assert.Equal(t, spec.Entries[2].Edge, frame.EdgeRight)
	assert.Equal(t, spec.Entries[2].Managed, true)
	assert.DeepEqual(t, spec.Entries[2].Command, []string{"btop"})
	assert.Equal(t, spec.Entries[2].Component, "")
}

// A frame entry has no siblings to share a ratio with, so a bare number is
// cells — the spelling IDEA.md's example uses — where muxctl reads it as a
// proportional weight.
func TestNormalize_SizeSpellings(t *testing.T) {
	for _, tc := range []struct {
		scalar string
		want   muxctl.Size
	}{
		{"2", muxctl.Size{N: 2, Absolute: true}},
		{`"2"`, muxctl.Size{N: 2, Absolute: true}},
		{"2c", muxctl.Size{N: 2, Absolute: true}},
		{"20%", muxctl.Size{N: 20, Percent: true}},
		{"100%", muxctl.Size{N: 100, Percent: true}},
	} {
		t.Run(tc.scalar, func(t *testing.T) {
			spec, err := normalize(t, "frame:\n  - edge: top\n    size: "+tc.scalar+
				"\n    component: statusbar\n")
			assert.NilError(t, err)
			assert.Equal(t, spec.Entries[0].Size, tc.want)
		})
	}
}

func TestNormalize_Errors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "no entries",
			content: "frame: []\n",
			want:    "no entries",
		},
		{
			name:    "missing frame key",
			content: "other: 1\n",
			want:    "no entries",
		},
		{
			name:    "unknown edge",
			content: "frame:\n  - edge: middle\n    size: 2\n    component: statusbar\n",
			want:    `unknown edge "middle"`,
		},
		{
			name:    "missing edge",
			content: "frame:\n  - size: 2\n    component: statusbar\n",
			want:    "edge is required",
		},
		{
			name:    "missing size",
			content: "frame:\n  - edge: top\n    component: statusbar\n",
			want:    "size is required",
		},
		{
			name: "both component and command",
			content: "frame:\n  - edge: top\n    size: 2\n    component: statusbar\n" +
				"    command: [btop]\n",
			want: "mutually exclusive",
		},
		{
			name:    "neither component nor command",
			content: "frame:\n  - edge: top\n    size: 2\n",
			want:    "one of component: or command: is required",
		},
		{
			name:    "unknown component",
			content: "frame:\n  - edge: top\n    size: 2\n    component: clock\n",
			want:    `unknown component "clock"`,
		},
		{
			name: "managed on a component entry",
			content: "frame:\n  - edge: top\n    size: 2\n    component: statusbar\n" +
				"    managed: true\n",
			want: "managed: applies to command: entries only",
		},
		{
			name:    "entry index in the message",
			content: "frame:\n  - edge: top\n    size: 2\n    component: statusbar\n  - edge: top\n",
			want:    "entry 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalize(t, tc.content)
			assert.Assert(t, err != nil, "expected an error")
			assert.Assert(t, cmp.Contains(err.Error(), tc.want))
		})
	}
}

// Sizes are rejected by the shared muxctl grammar, so the frame layer inherits
// its bounds.
func TestNormalize_InvalidSizes(t *testing.T) {
	for _, scalar := range []string{"0", "-1", "101%", "0%", "wide", `""`} {
		t.Run(scalar, func(t *testing.T) {
			_, err := frame.Decode(strings.NewReader(
				"frame:\n  - edge: top\n    size: " + scalar + "\n    component: statusbar\n",
			))
			assert.Assert(t, err != nil, "expected a decode error")
		})
	}
}

func TestNormalize_WarnsUnknownFields(t *testing.T) {
	raw, err := frame.Decode(strings.NewReader(`
name: mine
frame:
  - edge: top
    size: 2
    component: statusbar
    zorder: 3
    kolor: red
`))
	assert.NilError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := contextkey.WithSlogLogger(context.Background(), logger)

	spec, err := frame.Normalize(ctx, "/conf/frame/mine.yaml", raw)
	assert.NilError(t, err)
	assert.Equal(t, len(spec.Entries), 1)

	out := buf.String()
	assert.Assert(t, cmp.Contains(out, "ignoring unrecognized top-level field"))
	assert.Assert(t, cmp.Contains(out, "field=name"))
	assert.Assert(t, cmp.Contains(out, "ignoring unrecognized entry field"))
	assert.Assert(t, cmp.Contains(out, "entry=0"))
	// One warning per stray key, sorted.
	assert.Assert(t, strings.Index(out, "field=kolor") < strings.Index(out, "field=zorder"))
}

func TestBuiltinComponents(t *testing.T) {
	assert.DeepEqual(t, frame.BuiltinComponents(), []string{"statusbar", "switcher"})
	assert.Assert(t, frame.IsBuiltinComponent(frame.ComponentSwitcher))
	assert.Assert(t, !frame.IsBuiltinComponent("clock"))
}
