package model

import (
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"
)

func TestHookLayers_Resolve_Precedence(t *testing.T) {
	frame := HookSet{HookEventBell: {Action: HookActionPassthrough, Exec: []string{"frame"}}}
	command := HookSet{
		HookEventBell:  {Action: HookActionBlock, Exec: []string{"command"}},
		HookEventTitle: {Exec: []string{"title-command"}},
	}
	global := HookSet{
		HookEventBell:         {Exec: []string{"global"}},
		HookEventTitle:        {Action: HookActionBlock, Exec: []string{"title-global"}},
		HookEventNotification: {Action: HookActionBlock},
	}

	for _, tc := range []struct {
		name   string
		layers HookLayers
		event  HookEvent
		want   Hook
	}{
		{
			name:   "built-in default when no layer names the event",
			layers: HookLayers{},
			event:  HookEventBell,
			want:   Hook{},
		},
		{
			name:   "global applies when nothing more specific names it",
			layers: HookLayers{Command: command, Global: global},
			event:  HookEventNotification,
			want:   Hook{Action: HookActionBlock},
		},
		{
			name:   "per-command beats global",
			layers: HookLayers{Command: command, Global: global},
			event:  HookEventBell,
			want:   Hook{Action: HookActionBlock, Exec: []string{"command"}},
		},
		{
			// The seam phase 3 fills; nothing sets Frame today.
			name:   "frame beats per-command",
			layers: HookLayers{Frame: frame, Command: command, Global: global},
			event:  HookEventBell,
			want:   Hook{Action: HookActionPassthrough, Exec: []string{"frame"}},
		},
		{
			// Layers replace an event whole: the command entry leaves Action
			// unset, so the event passes through despite the global block.
			name:   "the winning layer defines the event completely",
			layers: HookLayers{Command: command, Global: global},
			event:  HookEventTitle,
			want:   Hook{Exec: []string{"title-command"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.layers.Resolve(tc.event)
			assert.Equal(t, got.Action, tc.want.Action)
			assert.DeepEqual(t, got.Exec, tc.want.Exec)
			assert.Equal(t, got.Blocks(), tc.want.Action == HookActionBlock)
		})
	}
}

func TestHookSet_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     HookSet
		wantErr string
	}{
		{name: "empty is valid"},
		{
			name: "known events and actions",
			set: HookSet{
				HookEventBell:         {Action: HookActionBlock},
				HookEventTitle:        {Action: HookActionPassthrough, Exec: []string{"notify"}},
				HookEventNotification: {Exec: []string{"notify", "--urgency", "low"}},
			},
		},
		{
			name:    "unknown event",
			set:     HookSet{HookEvent("belll"): {}},
			wantErr: `hooks: unknown event "belll" (known: bell, title, notification)`,
		},
		{
			name:    "unknown action",
			set:     HookSet{HookEventBell: {Action: HookAction("swallow")}},
			wantErr: `hooks: event "bell": unknown action "swallow" (known: passthrough, block)`,
		},
		{
			name:    "empty program",
			set:     HookSet{HookEventBell: {Exec: []string{"", "arg"}}},
			wantErr: `hooks: event "bell": exec program is empty`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.set.Validate()
			if tc.wantErr == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestCommandConfigHooksRoundTrip(t *testing.T) {
	cfg := CommandConfig{
		Argv:            []string{"sh"},
		Dir:             "/work",
		Env:             []string{"PATH=/bin"},
		RestartPolicy:   RestartPolicyNo,
		Tty:             true,
		ScrollbackBytes: 1024,
		LogDriver:       DefaultLogDriver,
		Hooks: HookSet{
			HookEventBell: {Action: HookActionBlock, Exec: []string{"notify-send", "bell"}},
		},
		CommandDir: "/data/cmd/1",
	}
	assert.NilError(t, cfg.Validate())

	data, err := json.Marshal(cfg)
	assert.NilError(t, err)

	var back CommandConfig
	assert.NilError(t, json.Unmarshal(data, &back))
	assert.DeepEqual(t, back.Hooks, cfg.Hooks)

	// An unset field stays out of the serialized form.
	cfg.Hooks = nil
	data, err = json.Marshal(cfg)
	assert.NilError(t, err)
	assert.Assert(t, !jsonHasKey(t, data, "hooks"))
}

func TestCommandConfigValidateRejectsBadHooks(t *testing.T) {
	cfg := CommandConfig{
		Argv:            []string{"sh"},
		Dir:             "/work",
		Env:             []string{"PATH=/bin"},
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: 1024,
		LogDriver:       DefaultLogDriver,
		Hooks:           HookSet{HookEvent("nope"): {}},
	}
	assert.ErrorContains(t, cfg.ValidateCreate(), `unknown event "nope"`)
}

func jsonHasKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	assert.NilError(t, json.Unmarshal(data, &m))
	_, ok := m[key]
	return ok
}
