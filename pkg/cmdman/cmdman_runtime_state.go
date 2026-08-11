package cmdman

import (
	"context"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"golang.org/x/sync/errgroup"
)

// Reported status values as they are spelled on the CLI and in --format
// templates. The wire form is an enum; these are its rendering.
const (
	ReportedStatusWorking = "working"
	ReportedStatusWaiting = "waiting"
	ReportedStatusDone    = "done"
)

// RuntimeState is the state a monitor holds for the run it is supervising: what
// the command said about itself through its output or through the status RPCs.
// It is never persisted - a command with no live monitor simply has none.
//
// CLI-output type; see InspectOutput for why it carries no json name tags.
type RuntimeState struct {
	Title      string
	Status     string `json:",omitzero"` // working, waiting, done, or empty
	Detail     string `json:",omitzero"`
	BellUnread bool
}

// RuntimeStatesOption tunes the fan-out of RuntimeStates. The zero value uses
// the defaults.
type RuntimeStatesOption struct {
	// Parallelism bounds concurrent dials. <= 0 uses defaultRuntimeStateDials.
	Parallelism int
	// Timeout bounds a single dial+call. <= 0 uses defaultRuntimeStateTimeout.
	Timeout time.Duration
}

const (
	defaultRuntimeStateDials   = 8
	defaultRuntimeStateTimeout = 300 * time.Millisecond
)

// RuntimeStatesBudget caps what a whole runtime-state fan-out may add to a
// listing. RuntimeStates does not apply it itself — its own timeout is per
// dial, which alone would let a long list of dead sockets add a batch of it per
// group of workers — so every caller that lists at a glance (`ls`, compose `ps`
// and `status`, the TUI refresh) hands it a context deadlined by this. A
// command whose monitor did not answer in time costs its own columns, not the
// listing's responsiveness.
const RuntimeStatesBudget = time.Second

// RuntimeStates dials the monitors of the given commands concurrently and
// returns their runtime state keyed by command ID.
//
// Every failure is an absent entry, never an error: listing commands is a
// read-only glance, and a monitor that died between the store read and the dial
// must cost the caller an empty column, not the whole listing. The per-dial
// timeout keeps one wedged monitor from holding up the rest.
func (s *Service) RuntimeStates(
	ctx context.Context,
	entries []store.CommandEntry,
	opt RuntimeStatesOption,
) map[string]RuntimeState {
	parallelism := opt.Parallelism
	if parallelism <= 0 {
		parallelism = defaultRuntimeStateDials
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = defaultRuntimeStateTimeout
	}

	// Indexed results instead of a shared map: the workers never meet.
	found := make([]*RuntimeState, len(entries))
	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(parallelism)
	for i, entry := range entries {
		if entry.StateJSON == nil || entry.StateJSON.SocketPath == "" {
			continue
		}
		state := entry.StateJSON
		eg.Go(func() error {
			found[i] = s.runtimeState(ctx, state, timeout)
			return nil
		})
	}
	_ = eg.Wait()

	out := make(map[string]RuntimeState, len(entries))
	for i, rs := range found {
		if rs != nil {
			out[entries[i].ID] = *rs
		}
	}
	return out
}

func (s *Service) runtimeState(
	ctx context.Context,
	state *model.CommandState,
	timeout time.Duration,
) *RuntimeState {
	// A per-dial budget, not a shared one: a slow monitor early in the list
	// must not eat the time left for the ones behind it.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := s.connectMonitor(ctx, state)
	if err != nil {
		return nil
	}
	defer conn.Close()

	resp, err := cmdmanv1pb.NewCommandMonitorServiceClient(conn).
		Status(ctx, &cmdmanv1pb.StatusRequest{})
	if err != nil || resp.RuntimeState == nil {
		return nil
	}
	rs := runtimeStateFromProto(resp.RuntimeState)
	return &rs
}

func runtimeStateFromProto(state *cmdmanv1pb.RuntimeState) RuntimeState {
	return RuntimeState{
		Title:      state.Title,
		Status:     reportedStatusName(state.Status),
		Detail:     state.Detail,
		BellUnread: state.BellUnread,
	}
}

func reportedStatusName(s cmdmanv1pb.ReportedStatus) string {
	switch s {
	case cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WORKING:
		return ReportedStatusWorking
	case cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING:
		return ReportedStatusWaiting
	case cmdmanv1pb.ReportedStatus_REPORTED_STATUS_DONE:
		return ReportedStatusDone
	default:
		return ""
	}
}
