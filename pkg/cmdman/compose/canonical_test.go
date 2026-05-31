package compose_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

func TestCanonicalize(t *testing.T) {
	spec := compose.ComposeSpec{
		ComposeFile: "/work/cmd-compose.yaml",
		Project:     "demo",
		WorkDir:     "/work",
		Commands: []compose.Command{
			{
				Name:            "web",
				Dir:             "/work/srv",
				Args:            []string{"sh", "-c", "echo hi"},
				Env:             []string{"A=1", "B=2"},
				Labels:          map[string]string{"tier": "front"},
				RestartPolicy:   model.RestartPolicyOnFailure,
				MaxRetries:      3,
				StopSignal:      "SIGTERM",
				Tty:             true,
				ScrollbackBytes: 4096,
				LogDriver:       logdriver.LogDriver("k8s-file"),
				LogOpts:         map[string]string{"path": "/var/log/web"},
				After: []compose.AfterSpec{
					{Name: "db", Condition: compose.ConditionCompletedSuccessfully},
				},
				GeneratedName: "deadbeef-demo-web",
			},
			{
				Name:          "db",
				Dir:           "/work",
				Args:          []string{"sh", "-c", "echo db"},
				RestartPolicy: model.RestartPolicyAlways,
			},
		},
	}

	got := compose.Canonicalize(spec)

	assert.Equal(t, got.Name, "demo")
	assert.Equal(t, got.WorkDir, "/work")
	assert.Equal(t, len(got.Commands), 2)

	web := got.Commands["web"]
	assert.Equal(t, web.Dir, "/work/srv")
	assert.DeepEqual(t, web.Args, []string{"sh", "-c", "echo hi"})
	assert.DeepEqual(t, web.Env, []string{"A=1", "B=2"})
	assert.DeepEqual(t, web.Labels, map[string]string{"tier": "front"})
	// on-failure with a positive cap recomposes to "on-failure:N".
	assert.Equal(t, web.RestartPolicy, "on-failure:3")
	assert.Equal(t, web.StopSignal, "SIGTERM")
	assert.Equal(t, web.Tty, true)
	assert.Equal(t, web.ScrollbackBytes, 4096)
	assert.Equal(t, web.LogDriver, "k8s-file")
	assert.DeepEqual(t, web.LogOpts, map[string]string{"path": "/var/log/web"})
	assert.Equal(t, web.After["db"].Condition, "completed_successfully")

	db := got.Commands["db"]
	// A policy with no retry cap renders bare.
	assert.Equal(t, db.RestartPolicy, "always")
	assert.Equal(t, len(db.After), 0)
}

func TestCanonicalizeRestartPolicyBareOnFailure(t *testing.T) {
	// "on-failure" with an unlimited (zero) cap renders bare, not "on-failure:0".
	spec := compose.ComposeSpec{
		Project: "p",
		WorkDir: "/w",
		Commands: []compose.Command{{
			Name:          "c",
			Dir:           "/w",
			Args:          []string{"true"},
			RestartPolicy: model.RestartPolicyOnFailure,
		}},
	}
	got := compose.Canonicalize(spec)
	assert.Equal(t, got.Commands["c"].RestartPolicy, "on-failure")
}
