package cmdman

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/cmdman/pkg/hrstr"
)

// CreateRequest defines a command creation request.
type CreateRequest struct {
	Name            string
	Dir             string
	Env             []string
	Labels          map[string]string
	RestartPolicy   model.RestartPolicy
	MaxRetries      int
	StopSignal      string
	AutoRemove      bool
	Tty             bool
	ScrollbackBytes int
	LogDriver       logdriver.LogDriver
	LogOpts         map[string]string
	Argv            []string
}

// CreateResult is the result of creating a command record.
type CreateResult struct {
	ID   string
	Name string
}

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
	cfg.Env = withCommandContextEnv(cfg.Env, s.cfg, id, commandDir)
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
	if err := store.WriteCommandConfig(cfg.CommandDir, cfg); err != nil {
		return nil, fmt.Errorf("materialize config: %w", err)
	}
	if err := st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}); err != nil {
		return nil, fmt.Errorf("insert state: %w", err)
	}

	s.emitEvent(model.Event{
		Time:  time.Now().UTC(),
		Type:  model.EventTypeCreated,
		ID:    id,
		Name:  req.Name,
		State: model.EventTypeCreated,
	})

	return &CreateResult{ID: id, Name: req.Name}, nil
}

func (s *Service) buildCommandConfig(req CreateRequest) *model.CommandConfig {
	restartPolicy := req.RestartPolicy
	if restartPolicy == "" {
		restartPolicy = model.RestartPolicyNo
	}
	stopSignal := req.StopSignal
	if stopSignal == "" {
		stopSignal = model.DefaultStopSignal
	} else {
		_, canonical, err := hrstr.ParseSignal(stopSignal)
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

	return &model.CommandConfig{
		Argv:            append([]string(nil), req.Argv...),
		Dir:             dir,
		Env:             env,
		RestartPolicy:   restartPolicy,
		MaxRetries:      req.MaxRetries,
		StopSignal:      stopSignal,
		Tty:             req.Tty,
		ScrollbackBytes: scrollbackBytes,
		LogDriver:       logDriver,
		LogOpts:         maps.Clone(req.LogOpts),
		Labels:          maps.Clone(req.Labels),
		Annotations:     annotations,
	}
}
