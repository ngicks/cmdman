package compose

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func TestStatusMergesRuntimeStateOntoProjectCommands(t *testing.T) {
	svc := &Service{svc: testCmdmanSvc{
		list: func(_ context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error) {
			assert.Assert(t, req.AllStates)
			assert.Equal(t, req.Labels[LabelProject], "proj")
			return []store.CommandEntry{
				buildTestProjectEntry(
					"id-worker", "worker", "proj", "/wd", "/wd/cmd-compose.yaml",
					model.EventTypeExited,
				),
				buildTestProjectEntry(
					"id-api", "api", "proj", "/wd", "/wd/cmd-compose.yaml",
					model.EventTypeRunning,
				),
			}, nil
		},
		runtime: func(
			_ context.Context,
			entries []store.CommandEntry,
		) map[string]cmdman.RuntimeState {
			assert.Equal(t, len(entries), 2)
			return map[string]cmdman.RuntimeState{
				"id-api": {
					Title:      "api server",
					Status:     cmdman.ReportedStatusWaiting,
					Detail:     "needs input",
					BellUnread: true,
				},
			}
		},
	}}

	states, err := svc.Status(
		context.Background(),
		ProjectSelection{Project: "proj", WorkDir: "/wd"},
		nil,
	)
	assert.NilError(t, err)

	// Sorted by compose command name: api before worker.
	assert.DeepEqual(t, states, []CommandRuntimeState{
		{
			Command:    "api",
			ID:         "id-api",
			State:      model.EventTypeRunning,
			Title:      "api server",
			Status:     cmdman.ReportedStatusWaiting,
			Detail:     "needs input",
			BellUnread: true,
		},
		// No live monitor: the runtime fields stay zero, the stored state does not.
		{Command: "worker", ID: "id-worker", State: model.EventTypeExited},
	})
}

// TestRuntimeStateFanOutIsBudgeted pins the overall cap both listings put on
// dialing the monitors: the timeout inside the fan-out is per socket, so a
// project whose monitors are all gone would otherwise spend it once per batch of
// workers — and `ps` is what completes a TAB press.
func TestRuntimeStateFanOutIsBudgeted(t *testing.T) {
	entries := []store.CommandEntry{
		buildTestProjectEntry(
			"id-api", "api", "proj", "/wd", "/wd/cmd-compose.yaml", model.EventTypeRunning,
		),
	}
	var deadlines []time.Time
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return entries, nil
		},
		runtime: func(
			ctx context.Context,
			_ []store.CommandEntry,
		) map[string]cmdman.RuntimeState {
			dl, ok := ctx.Deadline()
			assert.Assert(t, ok, "the fan-out must carry an overall deadline")
			deadlines = append(deadlines, dl)
			return nil
		},
	}}
	selection := ProjectSelection{Project: "proj", WorkDir: "/wd"}

	// The callers' contexts carry no deadline of their own: the budget belongs to
	// the listing, not to whoever remembered to set one.
	_, err := svc.Status(context.Background(), selection, nil)
	assert.NilError(t, err)
	_, err = svc.Ps(context.Background(), selection, nil)
	assert.NilError(t, err)

	assert.Equal(t, len(deadlines), 2)
	for _, dl := range deadlines {
		if d := time.Until(dl); d <= 0 || d > cmdman.RuntimeStatesBudget {
			t.Errorf("the fan-out may run for %s, want at most the %s budget",
				d, cmdman.RuntimeStatesBudget)
		}
	}
}

func TestStatusFiltersAndValidatesCommandNames(t *testing.T) {
	entries := []store.CommandEntry{
		buildTestProjectEntry(
			"id-api", "api", "proj", "/wd", "/wd/cmd-compose.yaml", model.EventTypeRunning,
		),
		buildTestProjectEntry(
			"id-worker", "worker", "proj", "/wd", "/wd/cmd-compose.yaml", model.EventTypeRunning,
		),
	}
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return entries, nil
		},
	}}
	selection := ProjectSelection{Project: "proj", WorkDir: "/wd"}

	states, err := svc.Status(context.Background(), selection, []string{"worker"})
	assert.NilError(t, err)
	assert.Equal(t, len(states), 1)
	assert.Equal(t, states[0].Command, "worker")

	_, err = svc.Status(context.Background(), selection, []string{"nope"})
	assert.ErrorContains(t, err, "unknown compose command(s)")
}
