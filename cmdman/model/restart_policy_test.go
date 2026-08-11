package model

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestParseRestartPolicy(t *testing.T) {
	tests := []struct {
		in         string
		wantPolicy RestartPolicy
		wantMax    int
		wantErr    bool
	}{
		{in: "no", wantPolicy: RestartPolicyNo},
		{in: "always", wantPolicy: RestartPolicyAlways},
		{in: "on-failure", wantPolicy: RestartPolicyOnFailure},
		{in: "on-failure:5", wantPolicy: RestartPolicyOnFailure, wantMax: 5},
		{in: "on-failure:0", wantPolicy: RestartPolicyOnFailure, wantMax: 0},
		{in: "on-failure:-1", wantErr: true},
		{in: "on-failure:abc", wantErr: true},
		{in: "on-failure:", wantErr: true},
		{in: "always:5", wantErr: true},
		{in: "no:1", wantErr: true},
		{in: "bogus", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			policy, max, err := ParseRestartPolicy(tt.in)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error for %q", tt.in)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, policy, tt.wantPolicy)
			assert.Equal(t, max, tt.wantMax)
		})
	}
}
