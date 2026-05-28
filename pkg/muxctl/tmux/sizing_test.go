package tmux

import (
	"slices"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

func sz(n int, absolute bool) muxctl.Size { return muxctl.Size{N: n, Absolute: absolute} }
func pct(n int) muxctl.Size               { return muxctl.Size{N: n, Percent: true} }

func TestComputeChildCells(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		parent int
		splits []muxctl.Size
		want   []int
	}{
		{
			name:   "two equal weights",
			parent: 21, // 21 - 1 separator = 20; split 1:1 -> 10, 10
			splits: []muxctl.Size{sz(1, false), sz(1, false)},
			want:   []int{10, 10},
		},
		{
			name:   "weights 1:2 with rounding handed to last weighted",
			parent: 11, // 11 - 1 = 10; 1/3*10=3, 2/3*10=6; remainder 1 -> last
			splits: []muxctl.Size{sz(1, false), sz(2, false)},
			want:   []int{3, 7},
		},
		{
			name:   "absolute reserves first; weights split the rest",
			parent: 101, // 101 - 90 - 2 separators = 9; 9/3 each
			splits: []muxctl.Size{sz(90, true), sz(1, false), sz(2, false)},
			want:   []int{90, 3, 6},
		},
		{
			name:   "all absolute; oversized leftover ignored",
			parent: 100,
			splits: []muxctl.Size{sz(30, true), sz(40, true)},
			want:   []int{30, 40},
		},
		{
			name:   "single child gets everything (no separator)",
			parent: 50,
			splits: []muxctl.Size{sz(1, false)},
			want:   []int{50},
		},
		{
			name:   "absolutes too big -> weighted children clamped to 0",
			parent: 30,
			splits: []muxctl.Size{sz(40, true), sz(1, false)},
			want:   []int{40, 0},
		},
		{
			name:   "empty splits returns empty",
			parent: 80,
			splits: nil,
			want:   []int{},
		},
		{
			name:   "percent reserves first; weights split the rest",
			parent: 102, // 50% of 102 = 51; 102 - 51 - 2 separators = 49; 49/(1+1)=24 r1 -> last gets 25
			splits: []muxctl.Size{pct(50), sz(1, false), sz(1, false)},
			want:   []int{51, 24, 25},
		},
		{
			name:   "100% percent leaves no room for weights",
			parent: 80,
			splits: []muxctl.Size{pct(100), sz(1, false)},
			want:   []int{80, 0},
		},
		{
			// abs 50 + pct 25%*200=50 + 3 separators = 103; leftover 97;
			// 1:1 -> 48 each, remainder 1 to last -> 48, 49.
			name:   "mixed absolute, percent and weight",
			parent: 200,
			splits: []muxctl.Size{sz(50, true), pct(25), sz(1, false), sz(1, false)},
			want:   []int{50, 50, 48, 49},
		},
		{
			name:   "percent rounds down",
			parent: 99, // 33% of 99 = 32.67 -> 32
			splits: []muxctl.Size{pct(33), sz(1, false)},
			want:   []int{32, 66}, // leftover = 99 - 32 - 1 = 66
		},
		{
			name:   "small parent with percent below one cell -> clamped to 0",
			parent: 10, // 5% of 10 = 0.5 -> 0
			splits: []muxctl.Size{pct(5), sz(1, false)},
			want:   []int{0, 9}, // 10 - 0 - 1 = 9
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeChildCells(tc.parent, tc.splits)
			if !slices.Equal(got, tc.want) {
				t.Errorf("computeChildCells(%d, %v) = %v, want %v",
					tc.parent, tc.splits, got, tc.want)
			}
		})
	}
}

func TestPickFocus(t *testing.T) {
	t.Parallel()

	leaf := func(name string, focus bool) muxctl.PaneSpec {
		return muxctl.PaneSpec{
			Leaf: muxctl.Leaf{Name: name, Cmd: []string{"./" + name}, Focus: focus},
		}
	}
	hbox := func(children ...muxctl.PaneSpec) muxctl.PaneSpec {
		splits := make([]muxctl.Size, len(children))
		for i := range splits {
			splits[i] = muxctl.Size{N: 1}
		}
		return muxctl.PaneSpec{
			Container: muxctl.Container{
				Dir:    muxctl.DirHorizontal,
				Splits: splits,
				Panes:  children,
			},
		}
	}

	cases := []struct {
		name string
		root muxctl.PaneSpec
		want string
	}{
		{
			name: "single leaf root",
			root: leaf("a", false),
			want: "a",
		},
		{
			name: "first leaf when no focus marker",
			root: hbox(leaf("a", false), leaf("b", false)),
			want: "a",
		},
		{
			name: "explicit focus wins over first leaf",
			root: hbox(leaf("a", false), leaf("b", true), leaf("c", false)),
			want: "b",
		},
		{
			name: "focus nested deep",
			root: hbox(leaf("a", false),
				hbox(leaf("b", false), leaf("c", true)),
				leaf("d", false),
			),
			want: "c",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickFocus(tc.root)
			if got != tc.want {
				t.Errorf("pickFocus = %q, want %q", got, tc.want)
			}
		})
	}
}
