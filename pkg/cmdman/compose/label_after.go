package compose

import (
	"encoding/json"
	"fmt"
)

func decodeAfterLabel(raw string) ([]AfterSpec, error) {
	if raw == "" {
		return nil, nil
	}
	var after []AfterSpec
	if err := json.Unmarshal([]byte(raw), &after); err != nil {
		return nil, fmt.Errorf("decode %s: %w", LabelAfter, err)
	}
	for _, dep := range after {
		if err := dep.Validate(); err != nil {
			return nil, fmt.Errorf("decode %s: %w", LabelAfter, err)
		}
	}
	return after, nil
}
