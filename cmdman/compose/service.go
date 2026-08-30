package compose

import (
	"context"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/store"
)

var _ cmdmanSvc = (*cmdman.Service)(nil)

// cmdmanSvc is the minimal interface the compose package needs from cmdman.Service.
// Defined here (consumer side) per the small-interface-at-consumer rule.
// *cmdman.Service satisfies this interface.
//
// Nothing here serves the frame verbs. The seam a default_frame's managed entry
// needs is [mux.FrameSvc], which no cmdman type answers to; it reaches compose
// as its own dependency instead (see [WithFrameSvc]), so mirroring the service
// stays a mirror of the service.
type cmdmanSvc interface {
	Config() cmdman.CmdmanConfig
	Start(ctx context.Context, idOrName string) error
	Wait(ctx context.Context, req cmdman.WaitRequest) ([]cmdman.WaitResult, error)
	List(ctx context.Context, req cmdman.ListRequest) ([]store.CommandEntry, error)
	Create(ctx context.Context, req cmdman.CreateRequest) (*cmdman.CreateResult, error)
	Remove(ctx context.Context, req cmdman.RemoveRequest) ([]cmdman.RemoveResult, error)
	Stop(ctx context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error)
	Signal(ctx context.Context, idOrName string, sig int32) error
	Logs(ctx context.Context, req cmdman.LogsRequest) (logdriver.Reader, error)
	Inspect(ctx context.Context, idOrName string) (*cmdman.InspectOutput, error)
	Events(ctx context.Context, req cmdman.EventsRequest) (*cmdman.EventsSubscription, error)
	SendKeys(ctx context.Context, idOrName string, req cmdman.SendKeysRequest) error
	CaptureScreen(
		ctx context.Context,
		idOrName string,
		req cmdman.CaptureScreenRequest,
	) ([]byte, error)
	RecordComposeHistory(ctx context.Context, req cmdman.ComposeHistoryRequest) error
	RuntimeStates(
		ctx context.Context,
		entries []store.CommandEntry,
		opt cmdman.RuntimeStatesOption,
	) map[string]cmdman.RuntimeState
}

// Service wraps a cmdmanSvc with compose-specific reconciliation logic.
// It is testable without the CLI.
type Service struct {
	svc cmdmanSvc
	// reporter receives lifecycle state-trace events during up/start/stop/down.
	// nil disables reporting (see report).
	reporter Reporter
	// frameSvc is the seam [Service.MuxUp] hands to mux for the default_frame it
	// shows (V9). nil is the "no service behind the frame verbs" value; see
	// [WithFrameSvc] for who sets it and what a def costs when nobody did.
	frameSvc mux.FrameSvc
}

// NewService constructs a compose.Service from an existing cmdman.Service.
// Options such as WithReporter customize the service.
//
// A nil svc is allowed and yields a Service with no underlying cmdman service
// (s.svc stays a genuine nil interface, not a typed-nil pointer). Such a Service
// is valid only for verbs that never consume the underlying service — e.g.
// [Service.MuxDown], whose teardown needs only the project identity, and
// [Service.MuxLs], which degrades its live replica counts to "unknown".
func NewService(svc *cmdman.Service, opts ...ServiceOption) *Service {
	s := &Service{}
	// Guard the assignment so a nil *cmdman.Service does not become a non-nil
	// interface holding a typed-nil pointer (which would defeat s.svc != nil).
	if svc != nil {
		s.svc = svc
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// runtimeStates dials the given commands' monitors under the overall budget
// every glance-style listing shares ([cmdman.RuntimeStatesBudget]). The dial
// timeout underneath is per socket, so a project whose monitors are all gone
// would otherwise spend it once per batch of workers — and `ps` completes a TAB
// press.
func (s *Service) runtimeStates(
	ctx context.Context,
	entries []store.CommandEntry,
) map[string]cmdman.RuntimeState {
	ctx, cancel := context.WithTimeout(ctx, cmdman.RuntimeStatesBudget)
	defer cancel()
	return s.svc.RuntimeStates(ctx, entries, cmdman.RuntimeStatesOption{})
}
