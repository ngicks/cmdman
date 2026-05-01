package cmdman

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cmdmanv1pb "github.com/ngicks/crabswarm/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/crabswarm/pkg/cmdman/store"
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
	ScrollbackBytes int
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

func (s *Service) Create(_ context.Context, req CreateRequest) (*CreateResult, error) {
	cfg := s.buildCommandConfig(req)
	if err := cfg.ValidateCreate(); err != nil {
		return nil, err
	}

	id := generateID()
	commandDir, err := s.cfg.CommandDir(id)
	if err != nil {
		return nil, err
	}
	cfg.CommandDir = commandDir
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if err := st.InsertCommandConfig(id, req.Name, cfg); err != nil {
		return nil, fmt.Errorf("insert config: %w", err)
	}
	if err := cfg.Write(); err != nil {
		return nil, fmt.Errorf("materialize config: %w", err)
	}
	if err := st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}); err != nil {
		return nil, fmt.Errorf("insert state: %w", err)
	}

	return &CreateResult{ID: id, Name: req.Name}, nil
}

func (s *Service) buildCommandConfig(req CreateRequest) *store.CommandConfigJSON {
	restartPolicy := req.RestartPolicy
	if restartPolicy == "" {
		restartPolicy = store.RestartPolicyNo
	}
	stopSignal := req.StopSignal
	if stopSignal == "" {
		stopSignal = store.DefaultStopSignal
	} else {
		_, canonical, err := store.ParseSignal(stopSignal)
		if err == nil {
			stopSignal = canonical
		}
	}

	dir := req.Dir
	if dir == "" {
		dir = s.cfg.DefaultWorkingDir
	}

	env := append([]string(nil), req.Env...)
	if len(env) == 0 {
		env = append(env, s.cfg.DefaultEnvironment...)
	}

	scrollbackBytes := req.ScrollbackBytes
	if scrollbackBytes == 0 {
		scrollbackBytes = s.cfg.DefaultScrollbackBytes
	}

	annotations := map[string]string(nil)
	if req.AutoRemove {
		annotations = map[string]string{store.AnnotationAutoRemove: "true"}
	}

	return &store.CommandConfigJSON{
		Argv:            append([]string(nil), req.Argv...),
		Dir:             dir,
		Env:             env,
		RestartPolicy:   restartPolicy,
		StopSignal:      stopSignal,
		ScrollbackBytes: scrollbackBytes,
		Labels:          cloneStringMap(req.Labels),
		Annotations:     annotations,
	}
}

func (s *Service) Start(_ context.Context, idOrName string) error {
	st, err := s.openStore(true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	id, _, cfg, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return fmt.Errorf("get command config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("get command config: %w", err)
	}

	state, _, _, err := st.GetCommandState(id)
	if err != nil {
		return fmt.Errorf("get command state: %w", err)
	}
	switch state {
	case store.StateCreated, store.StateExited:
	default:
		return fmt.Errorf("command %s is in state %q, must be %q or %q", idOrName, state, store.StateCreated, store.StateExited)
	}

	if _, err := SpawnMonitor(s.cfg, id); err != nil {
		return fmt.Errorf("spawn monitor: %w", err)
	}
	if finalState, err := WaitForState(st, id, store.StateRunning, 100); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if finalState == store.StateExited {
			return nil
		}
		return fmt.Errorf("%w (state: %s)", err, finalState)
	}
	return nil
}

func (s *Service) ResolveMonitor(_ context.Context, idOrName string) (*MonitorEndpoint, error) {
	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	id, name, _, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	if stateJSON.SocketPath == "" {
		return nil, fmt.Errorf("no socket path for command %s", id)
	}

	return &MonitorEndpoint{
		ID:         id,
		Name:       name,
		SocketPath: stateJSON.SocketPath,
	}, nil
}

func (s *Service) OpenAttachSession(
	ctx context.Context,
	idOrName string,
) (*Session, error) {
	endpoint, err := s.ResolveMonitor(ctx, idOrName)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(
		"unix://"+endpoint.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to monitor: %w", err)
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

func (s *Service) List(_ context.Context, req ListRequest) ([]store.CommandEntry, error) {
	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if err := CleanStaleEntries(st, s.cfg); err != nil {
		return nil, fmt.Errorf("clean stale entries: %w", err)
	}

	entries, err := st.ListCommands(req.AllStates, req.Labels)
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return entries, nil
}

func (s *Service) Inspect(ctx context.Context, idOrName string) (*InspectOutput, error) {
	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	id, name, cfg, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	state, exitCode, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	exitHistory, _ := st.GetExitHistory(id)

	out := &InspectOutput{
		ID:          id,
		Name:        name,
		Config:      cfg,
		State:       state,
		ExitCode:    exitCode,
		StateJSON:   stateJSON,
		ExitHistory: exitHistory,
		ConfigPath:  cfg.ConfigPath(),
	}

	if stateJSON.SocketPath != "" {
		if live := getLiveStatus(ctx, stateJSON.SocketPath); live != nil {
			out.LiveStatus = live
		}
	}
	return out, nil
}

func (s *Service) Signal(ctx context.Context, idOrName string, sig int32) error {
	st, err := s.openStore(true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	id, err := st.ResolveID(idOrName)
	if err != nil {
		return fmt.Errorf("resolve command: %w", err)
	}
	if err := signalOne(ctx, st, id, sig); err != nil {
		return fmt.Errorf("signal command %s: %w", idOrName, err)
	}
	return nil
}

func (s *Service) Stop(ctx context.Context, req StopRequest) ([]CommandActionResult, error) {
	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	ids, err := resolveTargets(st, req.Targets, nil)
	if err != nil {
		return nil, err
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	results := make([]CommandActionResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, CommandActionResult{
			ID:  id,
			Err: s.stopTarget(ctx, st, id, req.Signal, timeout),
		})
	}
	return results, nil
}

func (s *Service) stopTarget(ctx context.Context, st *store.Store, id string, signalOverride string, timeout time.Duration) error {
	_, _, cfg, err := st.GetCommandConfig(id)
	if err != nil {
		return fmt.Errorf("get command config: %w", err)
	}

	effective := cfg.StopSignal
	if signalOverride != "" {
		effective = signalOverride
	}
	if effective == "" {
		effective = store.DefaultStopSignal
	}
	sig, _, err := store.ParseSignal(effective)
	if err != nil {
		return err
	}

	if err := stopOne(ctx, st, id, sig); err != nil {
		return err
	}
	if err := waitForStopped(ctx, st, id, timeout); err == nil {
		return nil
	} else if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	killSig, _, _ := store.ParseSignal("SIGKILL")
	if err := stopOne(ctx, st, id, killSig); err != nil {
		return fmt.Errorf("timeout waiting for stop, and SIGKILL failed: %w", err)
	}
	if err := waitForStopped(ctx, st, id, timeout); err != nil {
		return fmt.Errorf("timeout waiting for stop after SIGKILL: %w", err)
	}
	return nil
}

func (s *Service) Remove(ctx context.Context, req RemoveRequest) ([]CommandActionResult, error) {
	st, err := s.openStore(true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	ids, err := resolveTargets(st, req.Targets, req.Labels)
	if err != nil {
		return nil, err
	}

	results := make([]CommandActionResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, CommandActionResult{
			ID:  id,
			Err: rmOne(ctx, s.cfg, st, id, req.Force),
		})
	}
	return results, nil
}

func (s *Service) Migrate(_ context.Context) error {
	st, err := s.openStore(false)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Service) openStore(validate bool) (*store.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		return s.store, nil
	}
	dbPath, err := s.cfg.DBPath()
	if err != nil {
		return nil, err
	}
	s.store, err = store.OpenStore(dbPath, validate)
	return s.store, err
}

func getLiveStatus(ctx context.Context, sockPath string) *LiveStatusInfo {
	conn, err := grpc.NewClient(
		"unix://"+sockPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	resp, err := client.Status(ctx, &cmdmanv1pb.StatusRequest{})
	if err != nil {
		return nil
	}
	return &LiveStatusInfo{
		State:    resp.State,
		ExitCode: resp.ExitCode,
		PID:      resp.Pid,
	}
}

func stopOne(ctx context.Context, st *store.Store, id string, sig int32) error {
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}
	if stateJSON.SocketPath == "" {
		return fmt.Errorf("no socket path")
	}

	conn, err := grpc.NewClient(
		"unix://"+stateJSON.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	_, err = client.Stop(ctx, &cmdmanv1pb.StopRequest{Signal: sig})
	return err
}

func signalOne(ctx context.Context, st *store.Store, id string, sig int32) error {
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}
	if stateJSON.SocketPath == "" {
		return fmt.Errorf("no socket path")
	}

	conn, err := grpc.NewClient(
		"unix://"+stateJSON.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	_, err = client.Signal(ctx, &cmdmanv1pb.SignalRequest{Signal: sig})
	return err
}

func waitForStopped(ctx context.Context, st *store.Store, id string, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		state, _, _, err := st.GetCommandState(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if state == store.StateExited || state == store.StateFailed {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func rmOne(_ context.Context, cfg CmdmanConfig, st *store.Store, id string, force bool) error {
	state, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}

	if state == store.StateRunning || state == store.StateStarting {
		if !force {
			return fmt.Errorf("command is %s, use --force to remove", state)
		}
		if stateJSON.MonitorPID > 0 {
			proc, err := os.FindProcess(stateJSON.MonitorPID)
			if err == nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
	}

	_, _, commandCfg, err := st.GetCommandConfig(id)
	if err != nil {
		return err
	}

	if err := st.DeleteCommand(id); err != nil {
		return fmt.Errorf("delete from db: %w", err)
	}
	if commandCfg.CommandDir != "" {
		_ = os.RemoveAll(commandCfg.CommandDir)
	}
	runtimeDir, err := cfg.MonitorRuntimeDir(id)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(runtimeDir)
	return nil
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
