package compose

import (
	"context"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

var _ cmdmanSvc = (*cmdman.Service)(nil)

// cmdmanSvc is the minimal interface the compose package needs from cmdman.Service.
// Defined here (consumer side) per the small-interface-at-consumer rule.
// *cmdman.Service satisfies this interface.
type cmdmanSvc interface {
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
	OpenAttachSession(ctx context.Context, idOrName string) (*cmdman.Session, error)
	SendKeys(ctx context.Context, idOrName string, req cmdman.SendKeysRequest) error
}

// Service wraps a cmdmanSvc with compose-specific reconciliation logic.
// It is testable without the CLI.
type Service struct {
	svc cmdmanSvc
	// reporter receives lifecycle state-trace events during up/start/stop/down.
	// nil disables reporting (see report).
	reporter Reporter
}

// NewService constructs a compose.Service from an existing cmdman.Service.
// Options such as WithReporter customize the service.
func NewService(svc *cmdman.Service, opts ...ServiceOption) *Service {
	s := &Service{svc: svc}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
