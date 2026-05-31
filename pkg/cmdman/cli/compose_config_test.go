package cli

import (
	"bytes"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

func TestRenderComposeConfigCanonicalYAML(t *testing.T) {
	spec := compose.CanonicalSpec{
		Name:    "demo",
		WorkDir: "/work",
		Commands: map[string]compose.CanonicalCommand{
			// Insertion order is "web" then "db"; the encoder must sort keys so
			// "db" comes first regardless.
			"web": {
				Dir:           "/work/srv",
				Args:          []string{"sh", "-c", "echo hi"},
				Env:           []string{"A=1", "B=2"},
				RestartPolicy: "on-failure:3",
				After:         map[string]compose.CanonicalAfter{"db": {Condition: "completed"}},
			},
			"db": {
				Dir:  "/work",
				Args: []string{"sh", "-c", "echo db"},
			},
		},
	}

	var buf bytes.Buffer
	assert.NilError(t, RenderComposeConfig(&buf, spec))

	want := `name: demo
work_dir: /work
commands:
  db:
    dir: /work
    args:
      - sh
      - -c
      - echo db
  web:
    dir: /work/srv
    args:
      - sh
      - -c
      - echo hi
    env:
      - A=1
      - B=2
    restart_policy: on-failure:3
    after:
      db:
        condition: completed
`
	assert.Equal(t, buf.String(), want)

	// A second render of the same spec is byte-identical (deterministic).
	var buf2 bytes.Buffer
	assert.NilError(t, RenderComposeConfig(&buf2, spec))
	assert.Equal(t, buf2.String(), buf.String())
}
