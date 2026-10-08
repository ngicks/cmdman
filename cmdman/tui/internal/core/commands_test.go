package core_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
	"github.com/ngicks/cmdman/cmdman/tui/internal/coretest"
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

// A teardown still under way says the stops it has made alone, so its line
// never reads like the finished one.
func TestComposeDownProgressMsgStatus(t *testing.T) {
	msg := core.ComposeDownProgressMsg{
		Name:    "p",
		Summary: core.DownSummary{Stopped: 2, Removed: 1, Running: true},
	}
	if got, want := msg.Status(), "compose down p: running… stopped 2"; got != want {
		t.Errorf("Status() = %q, want %q", got, want)
	}
}

var downTarget = core.DownTarget{Project: "p", Path: "/work/p/compose.yaml", WorkDir: "/work/p"}

func downCall() coretest.DownCall {
	return coretest.DownCall{Project: "p", Path: "/work/p/compose.yaml", Workdir: "/work/p"}
}

// The teardown is launched as a job and followed: each report it makes is a
// progress message whose Next waits for the one after, and its end is the
// ComposeDownMsg the widgets always took.
func TestComposeDownCmdLaunchesThenFollows(t *testing.T) {
	fb := &coretest.FakeBackend{
		ComposeDownProgress: []core.DownSummary{
			{Running: true},
			{Stopped: 1, Running: true},
		},
		ComposeDownSummary: core.DownSummary{Stopped: 1, Removed: 1},
	}
	msg := core.ComposeDownCmd(t.Context(), fb, downTarget)()

	var progress []core.DownSummary
	for {
		p, ok := msg.(core.ComposeDownProgressMsg)
		if !ok {
			break
		}
		if p.Target != downTarget {
			t.Fatalf("progress target = %+v, want %+v", p.Target, downTarget)
		}
		progress = append(progress, p.Summary)
		msg = p.Next()()
	}
	if !slices.Equal(progress, fb.ComposeDownProgress) {
		t.Errorf("progress = %+v, want %+v", progress, fb.ComposeDownProgress)
	}
	want := core.ComposeDownMsg{
		Name: "p", Target: downTarget, Summary: core.DownSummary{Stopped: 1, Removed: 1},
	}
	if msg != want {
		t.Errorf("end = %+v, want %+v", msg, want)
	}
	if !slices.Equal(fb.ComposeDowns, []coretest.DownCall{downCall()}) {
		t.Errorf("launches = %v, want the target", fb.ComposeDowns)
	}
	if !slices.Equal(fb.DownFollowed, []core.DownJob{{ID: "p.down"}}) {
		t.Errorf("followed = %v, want the launched job", fb.DownFollowed)
	}
}

// A launch that fails is the end, with nothing to follow.
func TestComposeDownCmdReportsAFailedLaunch(t *testing.T) {
	fb := &coretest.FakeBackend{ComposeDownLaunchErr: errors.New("no compose file")}
	msg := core.ComposeDownCmd(t.Context(), fb, downTarget)()
	end, ok := msg.(core.ComposeDownMsg)
	if !ok || end.Err == nil || end.Earlier {
		t.Fatalf("a failed launch should end the teardown with its error, got %+v", msg)
	}
	if len(fb.DownFollowed) != 0 {
		t.Errorf("nothing was launched, yet followed %v", fb.DownFollowed)
	}
}

// What a widget that just opened is told about its project's last teardown.
func TestLastComposeDownCmd(t *testing.T) {
	newBackend := func(job *core.DownJob) *coretest.FakeBackend {
		fb := &coretest.FakeBackend{
			ComposeDownProgress: []core.DownSummary{{Stopped: 1, Running: true}},
			ComposeDownSummary:  core.DownSummary{Stopped: 2, Removed: 2},
		}
		if job != nil {
			fb.DownJobs = map[core.DownTarget]core.DownJob{downTarget: *job}
		}
		return fb
	}

	t.Run("none", func(t *testing.T) {
		fb := newBackend(nil)
		if msg := core.LastComposeDownCmd(t.Context(), fb, downTarget)(); msg != nil {
			t.Errorf("a project that never went down should say nothing, got %+v", msg)
		}
		if !slices.Equal(fb.DownFinds, []coretest.DownCall{downCall()}) {
			t.Errorf("lookups = %v, want the target", fb.DownFinds)
		}
	})

	t.Run("over", func(t *testing.T) {
		fb := newBackend(&core.DownJob{ID: "j1", Finished: true})
		msg := core.LastComposeDownCmd(t.Context(), fb, downTarget)()
		want := core.ComposeDownMsg{
			Name:    "p",
			Target:  downTarget,
			Summary: core.DownSummary{Stopped: 2, Removed: 2},
			Earlier: true,
		}
		if msg != want {
			t.Errorf("msg = %+v, want %+v", msg, want)
		}
		if len(fb.ComposeDowns) != 0 {
			t.Errorf("a lookup must not launch, launches = %v", fb.ComposeDowns)
		}
	})

	t.Run("running", func(t *testing.T) {
		fb := newBackend(&core.DownJob{ID: "j1"})
		msg := core.LastComposeDownCmd(t.Context(), fb, downTarget)()
		p, ok := msg.(core.ComposeDownProgressMsg)
		if !ok {
			t.Fatalf("a running job should be followed, got %+v", msg)
		}
		end, ok := p.Next()().(core.ComposeDownMsg)
		if !ok || end.Earlier {
			t.Errorf("a job that ends while followed is news, got %+v", end)
		}
	})
}

// DownFollows keeps the latest report of each teardown under way, forgets one
// that ended, and leaves the value it was changed from as it was.
func TestDownFollows(t *testing.T) {
	var none core.DownFollows
	running := none.Progress(core.ComposeDownProgressMsg{
		Name: "p", Target: downTarget, Summary: core.DownSummary{Stopped: 3, Running: true},
	})
	if _, ok := none.Status(downTarget); ok {
		t.Errorf("recording a teardown changed the value it was recorded on")
	}
	status, ok := running.Status(downTarget)
	if !ok || status != "compose down p: running… stopped 3" {
		t.Errorf("Status() = %q, %v", status, ok)
	}
	other := core.DownTarget{Project: "p", WorkDir: "/work/other"}
	if _, ok := running.Status(other); ok {
		t.Errorf("a project of another directory is another teardown")
	}

	done := running.Done(downTarget)
	if _, ok := done.Status(downTarget); ok {
		t.Errorf("a teardown that ended is still followed")
	}
	if _, ok := running.Status(downTarget); !ok {
		t.Errorf("forgetting a teardown changed the value it was forgotten on")
	}
}
