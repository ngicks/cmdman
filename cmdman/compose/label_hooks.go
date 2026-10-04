package compose

import (
	"encoding/json"
	"fmt"
)

// decodeHooksLabel decodes the [LabelHooks] value of one stored entry. Every
// replica of a command carries its own copy of the label, so the caller passes
// the label of the replica it acts on.
func decodeHooksLabel(raw string) ([]LifecycleHook, error) {
	if raw == "" {
		return nil, nil
	}
	var hooks []LifecycleHook
	if err := json.Unmarshal([]byte(raw), &hooks); err != nil {
		return nil, fmt.Errorf("decode %s: %w", LabelHooks, err)
	}
	if err := validateLifecycleHooks(hooks); err != nil {
		return nil, fmt.Errorf("decode %s: %w", LabelHooks, err)
	}
	return hooks, nil
}
