package store

import (
	"context"
	"encoding/json"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store/gen/query"
)

// CommandEntry represents a joined row from CommandConfig and CommandState.
type CommandEntry struct {
	ID         string
	Name       string
	CreatedAt  string
	State      model.EventType
	ExitCode   *int
	ConfigJSON *model.CommandConfig
	StateJSON  *model.CommandState
}

// ListCommands lists commands, optionally filtering by state and labels.
func (s *Store) ListCommands(allStates bool, labels map[string]string) ([]CommandEntry, error) {
	statesJSON, err := json.Marshal([]string{
		string(model.EventTypeCreated),
		string(model.EventTypeStarting),
		string(model.EventTypeRunning),
	})
	if err != nil {
		return nil, err
	}
	labelsJSON, err := marshalLabels(labels)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListCommands(context.Background(), query.ListCommandsParams{
		AllStates:  allStates,
		States:     string(statesJSON),
		Labels:     labelsJSON,
		LabelCount: int64(len(labels)),
	})
	if err != nil {
		return nil, err
	}

	var entries []CommandEntry
	for _, r := range rows {
		e := CommandEntry{
			ID:        r.ID,
			CreatedAt: r.Createdat,
			State:     model.EventType(r.State),
		}
		if r.Name.Valid {
			e.Name = r.Name.String
		}
		if r.Exitcode.Valid {
			ec := int(r.Exitcode.Int64)
			e.ExitCode = &ec
		}
		e.ConfigJSON = &model.CommandConfig{}
		if err := json.Unmarshal([]byte(r.ConfigJson), e.ConfigJSON); err != nil {
			return nil, err
		}
		e.ConfigJSON.BackfillDefaults()
		e.StateJSON = &model.CommandState{}
		if err := json.Unmarshal([]byte(r.StateJson), e.StateJSON); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// FindByLabels returns command IDs matching all the given labels.
func (s *Store) FindByLabels(labels map[string]string) ([]string, error) {
	labelsJSON, err := marshalLabels(labels)
	if err != nil {
		return nil, err
	}
	return s.queries.FindByLabels(context.Background(), query.FindByLabelsParams{
		Labels:     labelsJSON,
		LabelCount: int64(len(labels)),
	})
}

// marshalLabels renders a label filter as a JSON object for the json_each
// queries, mapping a nil map to an empty object so json_each never receives a
// JSON null.
func marshalLabels(labels map[string]string) (string, error) {
	if labels == nil {
		labels = map[string]string{}
	}
	data, err := json.Marshal(labels)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
