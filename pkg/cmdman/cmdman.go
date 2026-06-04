// Package cmdman is the command-daemon service that backs the cmdman
// binary: it persists command definitions, spawns per-command monitor
// processes, and exposes control over a Unix-domain gRPC socket. The
// CLI under cmd/cmdman is a thin wiring layer on top of this package.
package cmdman

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/eventlog"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Service struct {
	cfg CmdmanConfig

	mu sync.Mutex
	// mutex guarded fields
	// No direct access
	store   *store.Store
	evtLog  *eventlog.Writer
	evtLogE error
}

// NewService constructs a Service from an already-normalized config.
func NewService(cfg CmdmanConfig) *Service {
	return &Service{cfg: cfg}
}

func isMonitorUnavailable(err error) bool {
	return status.Code(err) == codes.Unavailable
}

// eventLog lazily opens (and caches) the process-wide event log writer.
// Errors are remembered so a missing/un-writable log doesn't repeatedly
// surface in every operation, but emitEvent will only log and continue.
func (s *Service) eventLog() (*eventlog.Writer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evtLog != nil {
		return s.evtLog, nil
	}
	if s.evtLogE != nil {
		return nil, s.evtLogE
	}
	path, err := s.cfg.EventLogPath()
	if err != nil {
		s.evtLogE = err
		return nil, err
	}
	w, err := eventlog.NewWriter(path)
	if err != nil {
		s.evtLogE = err
		return nil, err
	}
	s.evtLog = w
	return w, nil
}

// emitEvent appends an event best-effort. Failures are logged but do not
// fail the calling operation. The logger is derived from ctx.
func (s *Service) emitEvent(ctx context.Context, e model.Event) {
	w, err := s.eventLog()
	if err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).
			WarnContext(ctx, "eventlog: open writer", "error", err)
		return
	}
	if err := w.Append(e); err != nil {
		contextkey.ValueSlogLoggerDefault(ctx).
			WarnContext(ctx, "eventlog: append", "type", e.Type, "id", e.ID, "error", err)
	}
}

func (s *Service) Config() CmdmanConfig {
	return s.cfg
}

// Close releases resources owned by the service.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.closeStoreNoLock(); err != nil {
		return err
	}
	return nil
}

func (s *Service) openStore(ctx context.Context, validate bool) (*store.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		return s.store, nil
	}
	dbPath, err := s.cfg.DBPath()
	if err != nil {
		return nil, err
	}
	s.store, err = store.OpenStore(ctx, dbPath, validate)
	return s.store, err
}

func (s *Service) closeStoreNoLock() error {
	if s.store == nil {
		return nil
	}
	err := s.store.Close()
	s.store = nil
	return err
}

func (s *Service) OpenAttachSession(
	ctx context.Context,
	idOrName string,
) (*Session, error) {
	conn, err := s.connectMonitorByName(ctx, idOrName)
	if err != nil {
		return nil, err
	}

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	stream, err := client.Attach(ctx)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("attach: %w", err)
	}

	return &Session{
		conn:   conn,
		client: client,
		stream: stream,
	}, nil
}

func (s *Service) connectMonitorByName(
	ctx context.Context,
	idOrName string,
) (*grpc.ClientConn, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, _, _, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}

	conn, err := s.connectMonitor(ctx, stateJSON)
	if err != nil {
		if idOrName != id {
			return nil, fmt.Errorf("%q(%q): %w", idOrName, id, err)
		}
		return nil, fmt.Errorf("%q: %w", id, err)
	}

	return conn, nil
}

func (s *Service) connectMonitor(
	_ context.Context,
	state *model.CommandState,
) (conn *grpc.ClientConn, err error) {
	// Hide transport details because we may add other transports later

	if state.SocketPath == "" {
		return nil, fmt.Errorf("no socket path for command")
	}

	conn, err = grpc.NewClient(
		"unix://"+state.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to monitor: %w", err)
	}

	// store conn

	return conn, nil
}

func resolveTargets(st *store.Store, args []string, labels map[string]string) ([]string, error) {
	var ids []string

	for _, a := range args {
		id, err := st.ResolveID(a)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", a, err)
		}
		ids = append(ids, id)
	}

	if len(labels) > 0 {
		labelIDs, err := st.FindByLabels(labels)
		if err != nil {
			return nil, fmt.Errorf("find by labels: %w", err)
		}
		ids = append(ids, labelIDs...)
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no commands specified")
	}
	return ids, nil
}

func generateID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
