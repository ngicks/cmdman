package muxctl_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

func TestParseSize(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			in   string
			want muxctl.Size
		}{
			{"1", muxctl.Size{N: 1}},
			{"15", muxctl.Size{N: 15}},
			{"1c", muxctl.Size{N: 1, Absolute: true}},
			{"90c", muxctl.Size{N: 90, Absolute: true}},
			{"1%", muxctl.Size{N: 1, Percent: true}},
			{"50%", muxctl.Size{N: 50, Percent: true}},
			{"100%", muxctl.Size{N: 100, Percent: true}},
		}
		for _, tc := range cases {
			got, err := muxctl.ParseSize(tc.in)
			assert.NilError(t, err, "ParseSize(%q)", tc.in)
			assert.Equal(t, got, tc.want, "ParseSize(%q)", tc.in)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		// All of these must wrap ErrInvalidSize.
		cases := []string{
			"",      // empty
			"0",     // zero weight
			"0c",    // zero cells
			"0%",    // zero percent
			"-1",    // negative
			"-3c",   // negative cells
			"-5%",   // negative percent
			"101%",  // percent > 100
			"200%",  // percent > 100
			"abc",   // not a number
			"1a",    // junk suffix
			"c",     // "c" with no number
			"%",     // bare percent
			"100c%", // mixed junk
			"1.5",   // float not supported
			" 1 ",   // surrounding whitespace not trimmed
		}
		for _, in := range cases {
			_, err := muxctl.ParseSize(in)
			assert.ErrorIs(t, err, muxctl.ErrInvalidSize, "ParseSize(%q)", in)
		}
	})
}

func TestSizeYamlRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []muxctl.Size{
		{N: 1},
		{N: 15},
		{N: 1, Absolute: true},
		{N: 90, Absolute: true},
		{N: 1, Percent: true},
		{N: 50, Percent: true},
		{N: 100, Percent: true},
	}
	for _, sz := range cases {
		// Marshal a struct containing the Size so we exercise both directions
		// through the YAML pipeline the way splits is encoded.
		out, err := yaml.Marshal(struct {
			S muxctl.Size `yaml:"s"`
		}{S: sz})
		assert.NilError(t, err, "yaml.Marshal(%+v)", sz)

		var got struct {
			S muxctl.Size `yaml:"s"`
		}
		assert.NilError(t, yaml.Unmarshal(out, &got), "yaml.Unmarshal(%q)", out)
		assert.Equal(t, got.S, sz, "round trip via %s", out)
	}
}

func TestSizeUnmarshalYaml_NonScalar(t *testing.T) {
	t.Parallel()

	// A sequence cannot decode into a scalar Size.
	const doc = "s: [1, 2]\n"
	var got struct {
		S muxctl.Size `yaml:"s"`
	}
	err := yaml.Unmarshal([]byte(doc), &got)
	assert.Assert(t, err != nil, "expected error decoding sequence into Size")
}

func TestDecode_Valid(t *testing.T) {
	t.Parallel()

	const doc = `
driver: tmux
driver_opt:
  socket: cmdman
layouts:
  - name: services
    root:
      dir: h
      splits: [90c, 1, 2]
      panes:
        - name: api
          cmd: [./api]
        - name: worker
          cmd: [./worker]
        - dir: v
          splits: [1, 1]
          panes:
            - name: redis
              cmd: [./redis]
            - name: db
              cmd: [./db]
              focus: true
  - name: minimal
    root:
      name: api
      cmd: [./api]
`
	want := muxctl.MuxSpec{
		Driver:    "tmux",
		DriverOpt: map[string]string{"socket": "cmdman"},
		Layouts: []muxctl.Layout{
			{
				Name: "services",
				Root: muxctl.PaneSpec{
					Container: muxctl.Container{
						Dir:    muxctl.DirHorizontal,
						Splits: []muxctl.Size{{N: 90, Absolute: true}, {N: 1}, {N: 2}},
						Panes: []muxctl.PaneSpec{
							{Leaf: muxctl.Leaf{Name: "api", Cmd: []string{"./api"}}},
							{Leaf: muxctl.Leaf{Name: "worker", Cmd: []string{"./worker"}}},
							{
								Container: muxctl.Container{
									Dir:    muxctl.DirVertical,
									Splits: []muxctl.Size{{N: 1}, {N: 1}},
									Panes: []muxctl.PaneSpec{
										{
											Leaf: muxctl.Leaf{
												Name: "redis",
												Cmd:  []string{"./redis"},
											},
										},
										{
											Leaf: muxctl.Leaf{
												Name:  "db",
												Cmd:   []string{"./db"},
												Focus: true,
											},
										},
									},
								},
							},
						},
					},
				},
			},
			{
				Name: "minimal",
				Root: muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: "api", Cmd: []string{"./api"}}},
			},
		},
	}

	got, err := muxctl.Decode(strings.NewReader(doc))
	assert.NilError(t, err)
	assert.DeepEqual(t, got, want)

	// Sanity: the round-tripped spec must validate.
	assert.NilError(t, got.Validate())
}

// TestDecode_UnknownTopLevelKey asserts that unknown keys at the top level
// are CAPTURED into MuxSpec.Unknown (not errored, not silently dropped) per
// the project YAML convention.
func TestDecode_UnknownTopLevelKey(t *testing.T) {
	t.Parallel()

	const doc = `
driver: tmux
foo: bar
layouts: []
`
	got, err := muxctl.Decode(strings.NewReader(doc))
	assert.NilError(t, err)
	assert.Equal(t, got.Unknown["foo"], any("bar"))
}

// TestDecode_UnknownPaneKey asserts that unknown keys at the pane level are
// captured into PaneSpec.Unknown. The "mode" key is a cmdman-layer concept,
// intentionally not part of muxctl.PaneSpec — so it must land in Unknown.
func TestDecode_UnknownPaneKey(t *testing.T) {
	t.Parallel()

	const doc = `
layouts:
  - name: l1
    root:
      name: a
      cmd: [./a]
      mode: logs
`
	got, err := muxctl.Decode(strings.NewReader(doc))
	assert.NilError(t, err)
	assert.Equal(t, len(got.Layouts), 1)
	assert.Equal(t, got.Layouts[0].Root.Unknown["mode"], any("logs"))
}

// TestDecode_UnknownLayoutKey asserts that unknown keys at the layout level
// are captured into Layout.Unknown.
func TestDecode_UnknownLayoutKey(t *testing.T) {
	t.Parallel()

	const doc = `
layouts:
  - name: l1
    description: a friendly note
    root: { name: a, cmd: [./a] }
`
	got, err := muxctl.Decode(strings.NewReader(doc))
	assert.NilError(t, err)
	assert.Equal(t, len(got.Layouts), 1)
	assert.Equal(t, got.Layouts[0].Unknown["description"], any("a friendly note"))
}

func TestDecode_InvalidSizeInSplits(t *testing.T) {
	t.Parallel()

	const doc = `
layouts:
  - name: l1
    root:
      dir: h
      splits: [101%, 1]
      panes:
        - { name: a, cmd: [./a] }
        - { name: b, cmd: [./b] }
`
	_, err := muxctl.Decode(strings.NewReader(doc))
	assert.ErrorIs(t, err, muxctl.ErrInvalidSize)
}

func TestPaneSpec_IsLeafIsContainer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		p        muxctl.PaneSpec
		wantLeaf bool
		wantCont bool
	}{
		{
			name:     "leaf",
			p:        muxctl.PaneSpec{Leaf: muxctl.Leaf{Name: "a", Cmd: []string{"./a"}}},
			wantLeaf: true,
		},
		{
			name: "container",
			p: muxctl.PaneSpec{
				Container: muxctl.Container{
					Dir:    muxctl.DirHorizontal,
					Splits: []muxctl.Size{{N: 1}},
					Panes: []muxctl.PaneSpec{
						{Leaf: muxctl.Leaf{Name: "a", Cmd: []string{"./a"}}},
					},
				},
			},
			wantCont: true,
		},
		{
			name: "both (Cmd + Dir)", // rejected by Validate
			p: muxctl.PaneSpec{
				Container: muxctl.Container{Dir: muxctl.DirHorizontal},
				Leaf:      muxctl.Leaf{Cmd: []string{"./a"}},
			},
		},
		{
			name: "empty", // rejected by Validate
			p:    muxctl.PaneSpec{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.p.IsLeaf(), tc.wantLeaf, "IsLeaf")
			assert.Equal(t, tc.p.IsContainer(), tc.wantCont, "IsContainer")
		})
	}
}
