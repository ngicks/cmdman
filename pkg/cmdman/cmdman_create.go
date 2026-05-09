package cmdman

import (
	"context"
	"fmt"
	"maps"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
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

	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
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

	logDriver := req.LogDriver
	if logDriver == "" {
		logDriver = s.cfg.DefaultLogDriver
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
		Tty:             req.Tty,
		ScrollbackBytes: scrollbackBytes,
		LogDriver:       logDriver,
		LogOpts:         maps.Clone(req.LogOpts),
		Labels:          maps.Clone(req.Labels),
		Annotations:     annotations,
	}
}
