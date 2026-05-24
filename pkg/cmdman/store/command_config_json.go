package store

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func CommandConfigPath(commandDir string) string {
	return filepath.Join(commandDir, ConfigFileName)
}

func WriteCommandConfig(commandDir string, cfg *model.CommandConfigJSON) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(commandDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(CommandConfigPath(commandDir), data, 0o644)
}

func ReadCommandConfig(commandDir string) (*model.CommandConfigJSON, error) {
	data, err := os.ReadFile(CommandConfigPath(commandDir))
	if err != nil {
		return nil, err
	}
	var cfg model.CommandConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	backfillCommandConfigDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func backfillCommandConfigDefaults(cfg *model.CommandConfigJSON) {
	model.BackfillCommandConfigDefaults(cfg)
}
