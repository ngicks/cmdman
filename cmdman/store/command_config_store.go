package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store/gen/query"
)

// InsertCommandConfig inserts a new CommandConfig row.
func (s *Store) InsertCommandConfig(id, name string, cfg *model.CommandConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.queries.InsertCommandConfig(context.Background(), query.InsertCommandConfigParams{
		ID:        id,
		Name:      nullableString(name),
		Createdat: time.Now().UTC().Format(time.RFC3339),
		Json:      string(data),
	})
}

// GetCommandConfig retrieves a CommandConfig by exact name, exact ID, or ID prefix.
// If the input matches multiple commands by prefix, an error is returned.
func (s *Store) GetCommandConfig(
	idOrName string,
) (id, name string, cfg *model.CommandConfig, err error) {
	resolvedID, err := s.ResolveID(idOrName)
	if err != nil {
		return "", "", nil, err
	}
	row, err := s.queries.GetCommandConfig(context.Background(), resolvedID)
	if err != nil {
		return "", "", nil, err
	}
	id = row.ID
	if row.Name.Valid {
		name = row.Name.String
	}
	cfg = &model.CommandConfig{}
	if err := json.Unmarshal([]byte(row.Json), cfg); err != nil {
		return "", "", nil, err
	}
	cfg.BackfillDefaults()
	return id, name, cfg, nil
}

// ResolveID resolves an ID prefix or exact name to a full command ID.
// If the input matches multiple commands by prefix, an error is returned.
func (s *Store) ResolveID(idOrName string) (string, error) {
	ctx := context.Background()
	if id, err := s.queries.ResolveIDByName(ctx, idOrName); err == nil {
		return id, nil
	}
	if id, err := s.queries.ResolveIDByID(ctx, idOrName); err == nil {
		return id, nil
	}

	matches, err := s.queries.ResolveIDByPrefix(ctx, idOrName)
	if err != nil {
		return "", err
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no command found matching %q", idOrName)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous ID prefix %q matches %d commands", idOrName, len(matches))
	}
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
