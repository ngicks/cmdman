package model

import (
	"encoding/json"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func validCommandConfig() CommandConfig {
	return CommandConfig{
		Argv:            []string{"sh"},
		Dir:             "/work",
		Env:             []string{"PATH=/bin"},
		RestartPolicy:   RestartPolicyNo,
		ScrollbackBytes: 1024,
		LogDriver:       DefaultLogDriver,
		CommandDir:      "/data/cmd/1",
	}
}

func TestCommandConfigStopFieldsRoundTrip(t *testing.T) {
	cfg := validCommandConfig()
	timeout := Duration(30 * time.Second)
	cfg.StopTimeout = &timeout
	cfg.StopCommand = &StopCommand{Args: []string{"kill", "-QUIT", "1"}}
	assert.NilError(t, cfg.Validate())

	data, err := json.Marshal(cfg)
	assert.NilError(t, err)
	var m map[string]json.RawMessage
	assert.NilError(t, json.Unmarshal(data, &m))
	assert.Equal(t, string(m["stop_timeout"]), `"30s"`)
	assert.Equal(t, string(m["stop_command"]), `{"args":["kill","-QUIT","1"]}`)

	var back CommandConfig
	assert.NilError(t, json.Unmarshal(data, &back))
	assert.DeepEqual(t, back.StopTimeout, cfg.StopTimeout)
	assert.DeepEqual(t, back.StopCommand, cfg.StopCommand)

	// Unset fields stay out of the serialized form.
	cfg.StopTimeout = nil
	cfg.StopCommand = nil
	data, err = json.Marshal(cfg)
	assert.NilError(t, err)
	assert.Assert(t, !jsonHasKey(t, data, "stop_timeout"))
	assert.Assert(t, !jsonHasKey(t, data, "stop_command"))
}

func TestCommandConfigValidateStopFields(t *testing.T) {
	zero := Duration(0)
	negative := Duration(-time.Second)
	for _, tc := range []struct {
		name    string
		mutate  func(c *CommandConfig)
		wantErr string
	}{
		{name: "unset is valid", mutate: func(*CommandConfig) {}},
		{
			name:    "zero timeout",
			mutate:  func(c *CommandConfig) { c.StopTimeout = &zero },
			wantErr: "stop_timeout must be positive: 0s",
		},
		{
			name:    "negative timeout",
			mutate:  func(c *CommandConfig) { c.StopTimeout = &negative },
			wantErr: "stop_timeout must be positive: -1s",
		},
		{
			name:    "stop command without args",
			mutate:  func(c *CommandConfig) { c.StopCommand = &StopCommand{} },
			wantErr: "stop_command needs a program name",
		},
		{
			name:    "stop command with an empty program name",
			mutate:  func(c *CommandConfig) { c.StopCommand = &StopCommand{Args: []string{"", "x"}} },
			wantErr: "stop_command needs a program name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validCommandConfig()
			tc.mutate(&cfg)
			err := cfg.ValidateCreate()
			if tc.wantErr == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
