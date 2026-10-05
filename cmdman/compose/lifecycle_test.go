package compose_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/go-common/contextkey"
)

// normalizeYAML writes content as a compose file, then decodes and normalizes
// it. A decode error is returned the same way as a normalize error.
func normalizeYAML(
	t *testing.T,
	ctx context.Context,
	content string,
) (compose.ComposeSpec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
	raw, err := compose.DecodeFile(path)
	if err != nil {
		return compose.ComposeSpec{}, err
	}
	return compose.Normalize(ctx, path, raw, compose.NormalizeOpts{})
}

// hooksYAML wraps the hooks: list body in a single-command compose file.
func hooksYAML(hooks string) string {
	return `name: hooks-test
commands:
  foo:
    args: ["./foo"]
    env:
      - GREETING=hello
    hooks:
` + hooks
}

func TestLifecycleHooksNormalize(t *testing.T) {
	spec, err := normalizeYAML(t, context.Background(), hooksYAML(`
      - name: notify
        start_post: ["notify-send", "foo ${GREETING}"]
        stop_post:
          args: ["notify-send", "foo stopped"]
          on_error: ignore
      - name: scratch
        resource: scratch
        create_pre: ["mktemp", "-d", "/dev/shm/foo.XXXX"]
        remove_post: ["sh", "-c", "rm -rf \"$$CMDMAN_COMPOSE_RESOURCE_VALUE\""]
`))
	assert.NilError(t, err)
	assert.Equal(t, len(spec.Commands), 1)

	want := []compose.LifecycleHook{
		{
			Name: "notify",
			Events: map[compose.LifecycleEvent]compose.LifecycleExec{
				// Event args interpolate against the command's env: like its args.
				compose.LifecycleStartPost: {
					Args:    []string{"notify-send", "foo hello"},
					OnError: compose.OnErrorFail,
				},
				compose.LifecycleStopPost: {
					Args:    []string{"notify-send", "foo stopped"},
					OnError: compose.OnErrorIgnore,
				},
			},
		},
		{
			Name:     "scratch",
			Resource: "scratch",
			Events: map[compose.LifecycleEvent]compose.LifecycleExec{
				compose.LifecycleCreatePre: {
					Args:    []string{"mktemp", "-d", "/dev/shm/foo.XXXX"},
					OnError: compose.OnErrorFail,
				},
				compose.LifecycleRemovePost: {
					Args:    []string{"sh", "-c", `rm -rf "$CMDMAN_COMPOSE_RESOURCE_VALUE"`},
					OnError: compose.OnErrorFail,
				},
			},
		},
	}
	assert.DeepEqual(t, spec.Commands[0].Hooks, want)
}

func TestLifecycleHooksNoHooks(t *testing.T) {
	spec, err := normalizeYAML(t, context.Background(), `name: hooks-test
commands:
  foo:
    args: ["./foo"]
`)
	assert.NilError(t, err)
	assert.Assert(t, spec.Commands[0].Hooks == nil)
}

func TestLifecycleHooksValidResourceShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hooks string
	}{
		{name: "acquire only", hooks: `
      - name: r
        resource: r
        create_pre: ["true"]
`},
		{name: "create_post with remove_pre", hooks: `
      - name: r
        resource: r
        create_post: ["true"]
        remove_pre: ["true"]
`},
		{name: "start_pre with stop_post", hooks: `
      - name: r
        resource: r
        start_pre: ["true"]
        stop_post: ["true"]
`},
		{name: "start_post with stop_pre", hooks: `
      - name: r
        resource: r
        start_post: ["true"]
        stop_pre: ["true"]
`},
		{name: "every event without a resource", hooks: `
      - name: all
        create_pre: ["true"]
        create_post: ["true"]
        start_pre: ["true"]
        start_post: ["true"]
        stop_pre: ["true"]
        stop_post: ["true"]
        remove_pre: ["true"]
        remove_post: ["true"]
`},
		{name: "resource key differs from the hook name", hooks: `
      - name: a
        resource: shared.key_1
        create_pre: ["true"]
      - name: b
        resource: other
        start_pre: ["true"]
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeYAML(t, context.Background(), hooksYAML(tc.hooks))
			assert.NilError(t, err)
		})
	}
}

func TestLifecycleHooksInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hooks string
		want  string
	}{
		{name: "missing name", hooks: `
      - start_pre: ["true"]
`, want: "hooks[0]: name is required"},
		{name: "duplicate name", hooks: `
      - name: a
        start_pre: ["true"]
      - name: a
        stop_post: ["true"]
`, want: `hook "a": name is used by more than one hook`},
		{name: "name with path separator", hooks: `
      - name: a/b
        start_pre: ["true"]
`, want: `hook name "a/b" must not contain path separators`},
		{name: "name with leading dot", hooks: `
      - name: .a
        start_pre: ["true"]
`, want: `hook name ".a" must not start with '.' or '-'`},
		{name: "no events", hooks: `
      - name: a
`, want: `hook "a": no lifecycle event is set`},
		{name: "empty argv", hooks: `
      - name: a
        start_pre: []
`, want: `hook "a": start_pre: args is empty`},
		{name: "mapping without args", hooks: `
      - name: a
        start_pre:
          on_error: ignore
`, want: `hook "a": start_pre: args is empty`},
		{name: "unknown on_error", hooks: `
      - name: a
        start_pre:
          args: ["true"]
          on_error: panic
`, want: `hook "a": start_pre: unknown on_error "panic"`},
		{name: "scalar event", hooks: `
      - name: a
        start_pre: "true"
`, want: "hook event must be an argv list or a mapping"},
		{name: "args interpolation", hooks: `
      - name: a
        start_pre: ["${CMDMAN_TEST_HOOK_UNSET_VAR:?must be set}"]
`, want: `hook "a": start_pre: args[0] interpolation`},
		{name: "two acquires", hooks: `
      - name: r
        resource: r
        create_pre: ["true"]
        start_pre: ["true"]
`, want: `resource "r": more than one acquire event (create_pre, start_pre)`},
		{name: "no acquire", hooks: `
      - name: r
        resource: r
        remove_post: ["true"]
`, want: `resource "r": no acquire event`},
		{name: "stop_post on a create_pre resource", hooks: `
      - name: r
        resource: r
        create_pre: ["true"]
        stop_post: ["true"]
`, want: `stop_post cannot release a resource acquired at create_pre`},
		{name: "remove_pre on a start_post resource", hooks: `
      - name: r
        resource: r
        start_post: ["true"]
        remove_pre: ["true"]
`, want: `remove_pre cannot release a resource acquired at start_post`},
		{name: "two releases", hooks: `
      - name: r
        resource: r
        create_pre: ["true"]
        remove_pre: ["true"]
        remove_post: ["true"]
`, want: `more than one release event (remove_pre, remove_post)`},
		{name: "invalid resource key", hooks: `
      - name: r
        resource: "a b"
        create_pre: ["true"]
`, want: `resource name "a b" must not contain whitespace`},
		{name: "duplicate resource key", hooks: `
      - name: a
        resource: r
        create_pre: ["true"]
      - name: b
        resource: r
        start_pre: ["true"]
`, want: `hook "b": resource "r" is already declared by hook "a"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeYAML(t, context.Background(), hooksYAML(tc.hooks))
			assert.Assert(t, cmp.ErrorContains(err, tc.want))
			if !strings.Contains(tc.want, "argv list or a mapping") {
				assert.Assert(t, cmp.ErrorContains(err, `command "foo"`))
			}
		})
	}
}

func TestLifecycleHooksUnknownFieldWarnings(t *testing.T) {
	var buf bytes.Buffer
	ctx := contextkey.WithSlogLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(&buf, nil)),
	)
	_, err := normalizeYAML(t, ctx, hooksYAML(`
      - name: a
        zeta: 1
        alpha: 2
        start_pre:
          args: ["true"]
          retries: 3
          backoff: 1s
        stop_post: ["true"]
`))
	assert.NilError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, len(lines), 4, "one warning per unknown key:\n%s", buf.String())
	for i, want := range []struct{ msg, field string }{
		{"ignoring unrecognized hook field", "field=alpha"},
		{"ignoring unrecognized hook field", "field=zeta"},
		{"ignoring unrecognized hook event field", "field=backoff"},
		{"ignoring unrecognized hook event field", "field=retries"},
	} {
		assert.Assert(t, cmp.Contains(lines[i], want.msg))
		assert.Assert(t, cmp.Contains(lines[i], want.field))
		assert.Assert(t, cmp.Contains(lines[i], "command=foo"))
		assert.Assert(t, cmp.Contains(lines[i], "hook=a"))
	}
	assert.Assert(t, cmp.Contains(lines[2], "event=start_pre"))
}

func TestExecCommandNameAndHolderName(t *testing.T) {
	assert.Equal(t,
		compose.ExecCommandName("abc-proj-foo-1", "notify", compose.LifecycleStartPost),
		"abc-proj-foo-1.hook.notify.start_post",
	)
	assert.Equal(t,
		compose.HolderName("abc-proj-foo-1", "scratch"),
		"abc-proj-foo-1.res.scratch",
	)
}

func hookedCommand() compose.Command {
	return compose.Command{
		Name: "api",
		Args: []string{"./api"},
		Dir:  "/work",
		Hooks: []compose.LifecycleHook{
			{
				Name: "notify",
				Events: map[compose.LifecycleEvent]compose.LifecycleExec{
					compose.LifecycleStartPost: {
						Args:    []string{"notify-send", "up"},
						OnError: compose.OnErrorFail,
					},
				},
			},
			{
				Name:     "scratch",
				Resource: "scratch",
				Events: map[compose.LifecycleEvent]compose.LifecycleExec{
					compose.LifecycleCreatePre: {
						Args:    []string{"mktemp", "-d"},
						OnError: compose.OnErrorFail,
					},
				},
			},
		},
	}
}

func TestHashChangesOnHooks(t *testing.T) {
	base, err := compose.Hash(hookedCommand())
	assert.NilError(t, err)

	noHooks := hookedCommand()
	noHooks.Hooks = nil
	hNoHooks, err := compose.Hash(noHooks)
	assert.NilError(t, err)
	assert.Assert(t, hNoHooks != base, "hash must change when hooks are added")

	for _, tc := range []struct {
		name   string
		mutate func(c *compose.Command)
	}{
		{name: "args", mutate: func(c *compose.Command) {
			c.Hooks[0].Events[compose.LifecycleStartPost] = compose.LifecycleExec{
				Args:    []string{"notify-send", "started"},
				OnError: compose.OnErrorFail,
			}
		}},
		{name: "on_error", mutate: func(c *compose.Command) {
			c.Hooks[0].Events[compose.LifecycleStartPost] = compose.LifecycleExec{
				Args:    []string{"notify-send", "up"},
				OnError: compose.OnErrorIgnore,
			}
		}},
		{name: "event", mutate: func(c *compose.Command) {
			e := c.Hooks[0].Events[compose.LifecycleStartPost]
			delete(c.Hooks[0].Events, compose.LifecycleStartPost)
			c.Hooks[0].Events[compose.LifecycleStartPre] = e
		}},
		{name: "name", mutate: func(c *compose.Command) { c.Hooks[0].Name = "notify2" }},
		{name: "resource", mutate: func(c *compose.Command) { c.Hooks[1].Resource = "tmp" }},
		{name: "order", mutate: func(c *compose.Command) {
			c.Hooks[0], c.Hooks[1] = c.Hooks[1], c.Hooks[0]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := hookedCommand()
			tc.mutate(&c)
			h, err := compose.Hash(c)
			assert.NilError(t, err)
			assert.Assert(t, h != base, "hash must change when hook %s changes", tc.name)
		})
	}
}

func TestHashHooksOnErrorDefault(t *testing.T) {
	explicit, err := compose.Hash(hookedCommand())
	assert.NilError(t, err)

	c := hookedCommand()
	c.Hooks[0].Events[compose.LifecycleStartPost] = compose.LifecycleExec{
		Args: []string{"notify-send", "up"},
	}
	implicit, err := compose.Hash(c)
	assert.NilError(t, err)
	assert.Equal(t, implicit, explicit, "an unset on_error must hash as fail")
}

func TestHashHooksSameForArgvAndMapForm(t *testing.T) {
	short, err := normalizeYAML(t, context.Background(), hooksYAML(`
      - name: a
        start_pre: ["true"]
`))
	assert.NilError(t, err)
	long, err := normalizeYAML(t, context.Background(), hooksYAML(`
      - name: a
        start_pre:
          args: ["true"]
          on_error: fail
`))
	assert.NilError(t, err)

	h1, err := compose.Hash(short.Commands[0])
	assert.NilError(t, err)
	h2, err := compose.Hash(long.Commands[0])
	assert.NilError(t, err)
	assert.Equal(t, h1, h2)
}

func TestCanonicalizeLifecycleHooks(t *testing.T) {
	cmd := hookedCommand()
	cmd.Hooks[0].Events[compose.LifecycleStopPost] = compose.LifecycleExec{
		Args:    []string{"notify-send", "down"},
		OnError: compose.OnErrorIgnore,
	}
	// An unset on_error renders as fail.
	cmd.Hooks[1].Events[compose.LifecycleRemovePost] = compose.LifecycleExec{
		Args: []string{"rm", "-rf"},
	}
	got := compose.Canonicalize(compose.ComposeSpec{
		Project:  "p",
		WorkDir:  "/work",
		Commands: []compose.Command{cmd},
	})

	want := []compose.CanonicalLifecycleHook{
		{
			Name: "notify",
			StartPost: &compose.CanonicalLifecycleExec{
				Args:    []string{"notify-send", "up"},
				OnError: "fail",
			},
			StopPost: &compose.CanonicalLifecycleExec{
				Args:    []string{"notify-send", "down"},
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
				Args:    []string{"rm", "-rf"},
				OnError: "fail",
			},
		},
	}
	assert.DeepEqual(t, got.Commands["api"].Hooks, want)

	noHooks := compose.Canonicalize(compose.ComposeSpec{
		Project:  "p",
		WorkDir:  "/work",
		Commands: []compose.Command{{Name: "api", Args: []string{"./api"}, Dir: "/work"}},
	})
	assert.Assert(t, noHooks.Commands["api"].Hooks == nil)
}
