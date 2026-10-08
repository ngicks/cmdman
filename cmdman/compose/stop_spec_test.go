package compose_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/go-common/contextkey"
)

// stopYAML appends fields to the only command of a compose file.
func stopYAML(fields string) string {
	return `name: stop-test
commands:
  foo:
    args: ["./foo"]
    env:
      - GREETING=hello
` + fields
}

func decodeYAML(t *testing.T, content string) (compose.RawComposeSpec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
	return compose.DecodeFile(path)
}

func TestStopDecode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		want   *compose.RawStopCommand
	}{
		{name: "absent", fields: ""},
		{
			name:   "sequence form",
			fields: "    stop: [true]\n",
			want:   &compose.RawStopCommand{Args: []string{"true"}},
		},
		{
			name: "mapping form",
			fields: `    stop:
      args: [kill, -QUIT, "1"]
`,
			want: &compose.RawStopCommand{Args: []string{"kill", "-QUIT", "1"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := decodeYAML(t, stopYAML(tc.fields))
			assert.NilError(t, err)
			assert.DeepEqual(t, raw.Commands["foo"].Stop, tc.want)
		})
	}
}

func TestStopDecodeInvalidForm(t *testing.T) {
	_, err := decodeYAML(t, stopYAML("    stop: \"true\"\n"))
	assert.ErrorContains(t, err, "stop must be an argv list or a mapping with args")
}

func TestStopNormalize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		want   []string
	}{
		{name: "absent", fields: ""},
		{
			name:   "sequence form",
			fields: "    stop: [true]\n",
			want:   []string{"true"},
		},
		{
			// Stop args interpolate against the command's env: like its args.
			name: "mapping form with interpolation",
			fields: `    stop:
      args: [echo, "${GREETING}"]
`,
			want: []string{"echo", "hello"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := normalizeYAML(t, context.Background(), stopYAML(tc.fields))
			assert.NilError(t, err)
			assert.DeepEqual(t, spec.Commands[0].Stop, tc.want)
		})
	}
}

func TestStopNormalizeInvalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields string
		want   string
	}{
		{name: "empty sequence", fields: "    stop: []\n", want: "stop: args is empty"},
		{
			name:   "mapping without args",
			fields: "    stop:\n      signal: TERM\n",
			want:   "stop: args is empty",
		},
		{
			name:   "args interpolation",
			fields: "    stop: [\"${CMDMAN_TEST_STOP_UNSET_VAR:?must be set}\"]\n",
			want:   "stop: args[0] interpolation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeYAML(t, context.Background(), stopYAML(tc.fields))
			assert.Assert(t, cmp.ErrorContains(err, tc.want))
			assert.Assert(t, cmp.ErrorContains(err, `command "foo"`))
		})
	}
}

func TestStopUnknownFieldWarnings(t *testing.T) {
	var buf bytes.Buffer
	ctx := contextkey.WithSlogLogger(
		context.Background(),
		slog.New(slog.NewTextHandler(&buf, nil)),
	)
	_, err := normalizeYAML(t, ctx, stopYAML(`    stop:
      args: ["true"]
      timeout: 5s
      on_error: ignore
`))
	assert.NilError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	assert.Equal(t, len(lines), 2, "one warning per unknown key:\n%s", buf.String())
	for i, field := range []string{"field=on_error", "field=timeout"} {
		assert.Assert(t, cmp.Contains(lines[i], "ignoring unrecognized stop field"))
		assert.Assert(t, cmp.Contains(lines[i], field))
		assert.Assert(t, cmp.Contains(lines[i], "command=foo"))
	}
}

func TestStopGracePeriodNormalize(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "absent", value: ""},
		{name: "integer seconds", value: "30", want: 30 * time.Second},
		{name: "quoted integer seconds", value: `"30"`, want: 30 * time.Second},
		{name: "duration", value: "1m30s", want: 90 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := ""
			if tc.value != "" {
				fields = "    stop_grace_period: " + tc.value + "\n"
			}
			spec, err := normalizeYAML(t, context.Background(), stopYAML(fields))
			assert.NilError(t, err)
			assert.Equal(t, spec.Commands[0].StopGracePeriod, tc.want)
		})
	}
}

func TestStopGracePeriodInvalid(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{value: "0", want: "timeout must be positive"},
		{value: "0s", want: "timeout must be positive"},
		{value: "-5s", want: "timeout must be positive"},
		{value: "soon", want: "parse timeout"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := normalizeYAML(
				t,
				context.Background(),
				stopYAML("    stop_grace_period: "+tc.value+"\n"),
			)
			assert.Assert(t, cmp.ErrorContains(
				err,
				`command "foo": invalid stop_grace_period "`+tc.value+`"`,
			))
			assert.Assert(t, cmp.ErrorContains(err, tc.want))
		})
	}
}

func stopCommand() compose.Command {
	return compose.Command{
		Name: "api",
		Args: []string{"./api"},
		Dir:  "/work",
	}
}

// The digest of a command that sets neither field is pinned by
// TestHashGoldenDigest, so this test only checks the fields' own effect.
func TestHashStopFields(t *testing.T) {
	base, err := compose.Hash(stopCommand())
	assert.NilError(t, err)

	emptyStop := stopCommand()
	emptyStop.Stop = []string{}
	h, err := compose.Hash(emptyStop)
	assert.NilError(t, err)
	assert.Equal(t, h, base, "an empty stop argv must hash as an absent one")

	for _, tc := range []struct {
		name   string
		mutate func(c *compose.Command)
	}{
		{name: "stop_grace_period", mutate: func(c *compose.Command) {
			c.StopGracePeriod = 30 * time.Second
		}},
		{name: "stop", mutate: func(c *compose.Command) {
			c.Stop = []string{"true"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := stopCommand()
			tc.mutate(&c)
			h, err := compose.Hash(c)
			assert.NilError(t, err)
			assert.Assert(t, h != base, "hash must change when %s is set", tc.name)
		})
	}

	a := stopCommand()
	a.StopGracePeriod = 2 * time.Second
	b := stopCommand()
	b.StopGracePeriod = 3 * time.Second
	ha, err := compose.Hash(a)
	assert.NilError(t, err)
	hb, err := compose.Hash(b)
	assert.NilError(t, err)
	assert.Assert(t, ha != hb, "hash must change when stop_grace_period changes")
}

func TestHashStopSameForArgvAndMapForm(t *testing.T) {
	short, err := normalizeYAML(t, context.Background(), stopYAML("    stop: [true]\n"))
	assert.NilError(t, err)
	long, err := normalizeYAML(t, context.Background(), stopYAML(`    stop:
      args: ["true"]
`))
	assert.NilError(t, err)

	h1, err := compose.Hash(short.Commands[0])
	assert.NilError(t, err)
	h2, err := compose.Hash(long.Commands[0])
	assert.NilError(t, err)
	assert.Equal(t, h1, h2)
}

func TestCanonicalizeStopFields(t *testing.T) {
	cmd := stopCommand()
	cmd.StopGracePeriod = 90 * time.Second
	cmd.Stop = []string{"kill", "-QUIT", "1"}
	got := compose.Canonicalize(compose.ComposeSpec{
		Project:  "p",
		WorkDir:  "/work",
		Commands: []compose.Command{cmd},
	})
	assert.Equal(t, got.Commands["api"].StopGracePeriod, "1m30s")
	assert.DeepEqual(t, got.Commands["api"].Stop, []string{"kill", "-QUIT", "1"})

	unset := compose.Canonicalize(compose.ComposeSpec{
		Project:  "p",
		WorkDir:  "/work",
		Commands: []compose.Command{stopCommand()},
	})
	assert.Equal(t, unset.Commands["api"].StopGracePeriod, "")
	assert.Assert(t, unset.Commands["api"].Stop == nil)
}
