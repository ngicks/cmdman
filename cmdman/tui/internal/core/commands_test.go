package core_test

import (
	"errors"
	"testing"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

func TestComposeDownMsgStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  core.ComposeDownMsg
		want string
	}{
		{
			name: "no forced kill",
			msg:  core.ComposeDownMsg{Name: "p", Summary: core.DownSummary{Stopped: 2, Removed: 2}},
			want: "compose down p: stopped 2, removed 2",
		},
		{
			name: "forced kills",
			msg: core.ComposeDownMsg{
				Name:    "p",
				Summary: core.DownSummary{Stopped: 2, Removed: 2, ForceKilled: 1},
			},
			want: "compose down p: stopped 2, removed 2, force-killed 1",
		},
		{
			name: "forced kills and a failure",
			msg: core.ComposeDownMsg{
				Name:    "p",
				Summary: core.DownSummary{Stopped: 1, Removed: 0, ForceKilled: 1},
				Err:     errors.New("boom"),
			},
			want: "compose down p: stopped 1, removed 0, force-killed 1: boom",
		},
		{
			name: "unreleased resources",
			msg: core.ComposeDownMsg{
				Name:    "p",
				Summary: core.DownSummary{Stopped: 2, Removed: 2, Unreleased: 2},
			},
			want: "compose down p: stopped 2, removed 2, unreleased 2",
		},
		{
			name: "forced kills, unreleased resources and a failure",
			msg: core.ComposeDownMsg{
				Name:    "p",
				Summary: core.DownSummary{Stopped: 2, Removed: 1, ForceKilled: 1, Unreleased: 1},
				Err:     errors.New("boom"),
			},
			want: "compose down p: stopped 2, removed 1, force-killed 1, unreleased 1: boom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.Status(); got != tc.want {
				t.Errorf("Status() = %q, want %q", got, tc.want)
			}
		})
	}
}
