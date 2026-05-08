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
	"maps"
	"sync"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

type Service struct {
	cfg CmdmanConfig

	mu sync.Mutex
	// mutex guarded fields
	// No direct access
	store *store.Store
}

// NewService constructs a Service from an already-normalized config.
func NewService(cfg CmdmanConfig) *Service {
	return &Service{cfg: cfg}
}

// Close releases resources owned by the service.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	err := s.store.Close()
	s.store = nil
	return err
}

func (s *Service) Config() CmdmanConfig {
	return s.cfg
}

// CreateRequest defines a command creation request.
type CreateRequest struct {
	Name            string
	Dir             string
	Env             []string
	Labels          map[string]string
	RestartPolicy   store.RestartPolicy
	StopSignal      string
	AutoRemove      bool
	Tty             bool
	ScrollbackBytes int
	LogDriver       store.LogDriver
	LogOpts         map[string]string
	Argv            []string
}

// CreateResult is the result of creating a command record.
type CreateResult struct {
	ID   string
	Name string
}

// ListRequest defines list filtering.
type ListRequest struct {
	AllStates bool
	Labels    map[string]string
}

// StopRequest defines a stop operation across explicit targets and/or labels.
type StopRequest struct {
	Targets []string
	Signal  string
	Timeout time.Duration
}

// RemoveRequest defines a remove operation across explicit targets and/or labels.
type RemoveRequest struct {
	Targets []string
	Labels  map[string]string
	Force   bool
}

// LogsRequest defines a log read operation.
type LogsRequest struct {
	IDOrName string
	Follow   bool
}

// WaitRequest defines a wait operation across explicit targets.
type WaitRequest struct {
	Targets   []string
	Condition string
	Interval  time.Duration
	Ignore    bool
}

// WaitResult reports per-command outcome of a Wait operation.
// ExitCode is nil when the command has not exited (e.g. when waiting for a
// non-terminal condition such as "running") or when the command has been
// removed from the store before any exit code was recorded.
type WaitResult struct {
	ID       string
	ExitCode *int
	Err      error
}

// Wait conditions accepted by Service.Wait. "stopped" is satisfied by either
// "exited" or "failed" states; the rest match the corresponding state
// verbatim.
const (
	WaitConditionStopped  = "stopped"
	WaitConditionCreated  = "created"
	WaitConditionStarting = "starting"
	WaitConditionRunning  = "running"
	WaitConditionExited   = "exited"
	WaitConditionFailed   = "failed"
)

// CommandActionResult reports per-command outcome for bulk operations.
type CommandActionResult struct {
	ID  string
	Err error
}

// MonitorEndpoint identifies the live monitor for a command.
type MonitorEndpoint struct {
	ID         string
	Name       string
	SocketPath string
}

// InspectOutput is the merged command definition, state, and history.
type InspectOutput struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name,omitempty"`
	Config      *store.CommandConfigJSON `json:"config"`
	State       string                   `json:"state"`
	ExitCode    *int                     `json:"exit_code,omitempty"`
	StateJSON   *store.CommandStateJSON  `json:"state_detail"`
	ExitHistory []store.ExitRecord       `json:"exit_history,omitempty"`
	ConfigPath  string                   `json:"config_path,omitempty"`
	LiveStatus  *LiveStatusInfo          `json:"live_status,omitempty"`
}

// LiveStatusInfo is the live status from the monitor gRPC Status RPC.
type LiveStatusInfo struct {
	State    string `json:"state"`
	ExitCode int32  `json:"exit_code"`
	PID      int32  `json:"pid"`
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

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	maps.Copy(dst, src)
	return dst
}

func generateID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
