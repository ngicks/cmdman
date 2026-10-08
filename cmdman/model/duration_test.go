package model

import (
	"encoding/json"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestDurationJSON(t *testing.T) {
	data, err := json.Marshal(Duration(90 * time.Second))
	assert.NilError(t, err)
	assert.Equal(t, string(data), `"1m30s"`)

	var d Duration
	assert.NilError(t, json.Unmarshal([]byte(`"30s"`), &d))
	assert.Equal(t, time.Duration(d), 30*time.Second)

	assert.ErrorContains(t, json.Unmarshal([]byte(`"30"`), &d), "parse duration")
	assert.ErrorContains(t, json.Unmarshal([]byte(`"soon"`), &d), "parse duration")
}
