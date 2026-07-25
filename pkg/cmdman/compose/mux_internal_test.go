package compose

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/cmdman/pkg/muxctl"

	// Link the tmux muxctl driver so mux.List/MuxLs can resolve "tmux" via
	// muxctl.LookupDriver in this test binary (the real binary links it from
	// cmd/cmdman/main.go).
	_ "github.com/ngicks/cmdman/pkg/muxctl/tmux"
)

// muxTestSelection returns a minimal ProjectSelection carrying a "mux:" section,
// enough to reach the leaf-resolution step of the compose mux Service methods
// without a tmux server.
func muxTestSelection() ProjectSelection {
	return ProjectSelection{
		Spec: &ComposeSpec{
			Project: "proj",
			WorkDir: "/work",
			Mux: &mux.Spec{
				Layouts: []mux.Layout{
					{Name: "solo", Root: mux.PaneSpec{Command: "web"}},
				},
			},
		},
		Project: "proj",
		WorkDir: "/work",
	}
}

// listErrSvc builds a Service whose List always fails, so MuxLeafResolver (and
// thus MuxUp / MuxCycleScale) returns before touching the multiplexer driver.
func listErrSvc(err error) *Service {
	return &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return nil, err
		},
	}}
}

// MuxUp resolves leaves (which lists project commands) before any tmux call, so
// a List failure surfaces from the Selection-driven resolver, confirming the
// option's Selection reaches the resolver.
func TestServiceMuxUp_LeafResolverError(t *testing.T) {
	wantErr := errors.New("list boom")
	svc := listErrSvc(wantErr)

	err := svc.MuxUp(context.Background(), MuxUpOption{Selection: muxTestSelection()})
	assert.Assert(t, errors.Is(err, wantErr))
}

// MuxCycleScale likewise resolves leaves before any tmux call.
func TestServiceMuxCycleScale_LeafResolverError(t *testing.T) {
	wantErr := errors.New("list boom")
	svc := listErrSvc(wantErr)

	_, err := svc.MuxCycleScale(context.Background(), MuxCycleScaleOption{
		Selection: muxTestSelection(),
		Command:   "web",
	})
	assert.Assert(t, errors.Is(err, wantErr))
}

// NewService(nil) must store a genuine nil interface (not a typed-nil pointer),
// so the s.svc != nil guards in the mux verbs behave.
func TestNewService_NilTolerant(t *testing.T) {
	s := NewService(nil)
	assert.Assert(t, s.svc == nil)
}

// MuxLs on a nil-service Service must still list windows and simply skip the
// replica-count resolution (the s.svc != nil guard), rather than panicking on a
// nil-interface method call. A nonexistent tmux socket yields an empty listing.
func TestServiceMuxLs_NilServiceDegrades(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A socket path with no server: ListWindows treats "error connecting"
	// as zero windows, so MuxLs returns an empty listing without a real error.
	socket := filepath.Join(t.TempDir(), "no-such-server.sock")
	sel := ProjectSelection{
		Spec: &ComposeSpec{
			Project: "proj",
			WorkDir: "/work",
			Mux: &mux.Spec{
				Driver: muxctl.DriverSpec{Name: "tmux", Socket: socket},
				Layouts: []mux.Layout{
					{Name: "solo", Root: mux.PaneSpec{Command: "web"}},
				},
			},
		},
		Project: "proj",
		WorkDir: "/work",
	}

	res, err := NewService(nil).MuxLs(context.Background(), MuxLsOption{Selection: sel})
	assert.NilError(t, err)
	assert.Equal(t, len(res.Windows), 0)
	assert.Equal(t, len(res.ReplicaCounts), 0)
	assert.DeepEqual(t, res.CycleTargets, []string{"web"})
}
