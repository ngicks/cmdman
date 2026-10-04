package cli

import (
	"bytes"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/compose"
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
				Hooks: []compose.CanonicalLifecycleHook{
					{
						Name: "notify",
						StartPost: &compose.CanonicalLifecycleExec{
							Args:    []string{"notify-send", "web started"},
							OnError: "fail",
						},
						StopPost: &compose.CanonicalLifecycleExec{
							Args:    []string{"notify-send", "web stopped"},
							OnError: "ignore",
						},
					},
					{
						Name:     "scratch",
						Resource: "scratch",
						CreatePre: &compose.CanonicalLifecycleExec{
							Args:    []string{"mktemp", "-d"},
							OnError: "fail",
						},
						RemovePost: &compose.CanonicalLifecycleExec{
							Args:    []string{"rm", "-rf", "dir"},
							OnError: "continue",
						},
					},
				},
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
    hooks:
      - name: notify
        start_post:
          args:
            - notify-send
            - web started
          on_error: fail
        stop_post:
          args:
            - notify-send
            - web stopped
          on_error: ignore
      - name: scratch
        resource: scratch
        create_pre:
          args:
            - mktemp
            - -d
          on_error: fail
        remove_post:
          args:
            - rm
            - -rf
            - dir
          on_error: continue
`
	assert.Equal(t, buf.String(), want)

	// A second render of the same spec is byte-identical (deterministic).
	var buf2 bytes.Buffer
	assert.NilError(t, RenderComposeConfig(&buf2, spec))
	assert.Equal(t, buf2.String(), buf.String())
}
