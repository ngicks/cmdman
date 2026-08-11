package mux

import (
	"testing"

	"gotest.tools/v3/assert"
)

// TestDecodeScalePositions covers the space-joined "name=pos" decoder,
// including the malformed-pair cases it must skip gracefully.
func TestDecodeScalePositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want map[string]int
	}{
		{name: "empty string → nil", raw: "", want: nil},
		{name: "whitespace only → nil", raw: "   ", want: nil},
		{name: "single pair", raw: "web=2", want: map[string]int{"web": 2}},
		{
			name: "two pairs",
			raw:  "web=2 worker=1",
			want: map[string]int{"web": 2, "worker": 1},
		},
		{
			name: "extra whitespace between pairs",
			raw:  "  web=2   worker=1  ",
			want: map[string]int{"web": 2, "worker": 1},
		},
		{
			name: "malformed pairs skipped, valid kept",
			raw:  "web=2 bogus =3 empty= neg=-1 zero=0 nan=x worker=1",
			want: map[string]int{"web": 2, "worker": 1},
		},
		{name: "all malformed → nil", raw: "bogus =3 empty= nan=x", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decodeScalePositions(tc.raw)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

// TestEncodeScalePositions covers the encoder: sorted keys, pos<=0 skipping,
// and the empty-result cases.
func TestEncodeScalePositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    map[string]int
		want string
	}{
		{name: "nil map → empty", m: nil, want: ""},
		{name: "empty map → empty", m: map[string]int{}, want: ""},
		{name: "single entry", m: map[string]int{"web": 2}, want: "web=2"},
		{
			name: "sorted by key",
			m:    map[string]int{"worker": 1, "web": 2, "api": 3},
			want: "api=3 web=2 worker=1",
		},
		{
			name: "non-positive entries skipped",
			m:    map[string]int{"web": 2, "gone": 0, "neg": -5},
			want: "web=2",
		},
		{name: "all skipped → empty", m: map[string]int{"a": 0, "b": -1}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, encodeScalePositions(tc.m), tc.want)
		})
	}
}

// TestScaleCodecRoundTrip exercises the decode→modify→encode path that the
// cycle_scale read-modify-write depends on: a stored raw value is decoded,
// a command is added/removed, and re-encoded to the wire format.
func TestScaleCodecRoundTrip(t *testing.T) {
	t.Parallel()

	// Merge: decode existing, add a new command, re-encode.
	m := decodeScalePositions("web=2")
	if m == nil {
		m = make(map[string]int)
	}
	m["worker"] = 1
	assert.Equal(t, encodeScalePositions(m), "web=2 worker=1")

	// Removal: decode, delete a command, re-encode down to a single pair.
	m = decodeScalePositions("web=2 worker=1")
	delete(m, "worker")
	assert.Equal(t, encodeScalePositions(m), "web=2")

	// Full clear: removing the last command encodes to "" (caller unsets).
	m = decodeScalePositions("web=2")
	delete(m, "web")
	assert.Equal(t, encodeScalePositions(m), "")
}
