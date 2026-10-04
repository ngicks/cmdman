package compose

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestBuildLabelsStoresHooks(t *testing.T) {
	hooks := []LifecycleHook{
		{
			Name: "notify",
			Events: map[LifecycleEvent]LifecycleExec{
				LifecycleStartPost: {Args: []string{"notify-send", "up"}, OnError: OnErrorFail},
				LifecycleStopPost: {
					Args:    []string{"notify-send", "down"},
					OnError: OnErrorIgnore,
				},
			},
		},
		{
			Name:     "scratch",
			Resource: "scratch",
			Events: map[LifecycleEvent]LifecycleExec{
				LifecycleCreatePre:  {Args: []string{"mktemp", "-d"}, OnError: OnErrorFail},
				LifecycleRemovePost: {Args: []string{"rm", "-rf"}, OnError: OnErrorContinue},
			},
		},
	}
	cmd := reconcileCmd("api")
	cmd.Hooks = hooks

	labels := BuildLabels(reconcileSpec(cmd), cmd, "sha256:test", 1)
	raw, ok := labels[LabelHooks]
	assert.Assert(t, ok, "expected stored hooks label")

	got, err := decodeHooksLabel(raw)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, hooks)
}

func TestBuildLabelsOmitsHooksWhenNone(t *testing.T) {
	cmd := reconcileCmd("api")
	labels := BuildLabels(reconcileSpec(cmd), cmd, "sha256:test", 1)
	_, ok := labels[LabelHooks]
	assert.Assert(t, !ok, "a command without hooks must not carry the hooks label")

	got, err := decodeHooksLabel("")
	assert.NilError(t, err)
	assert.Assert(t, got == nil)
}

func TestDecodeHooksLabelRejectsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "not json", raw: "{", want: "decode " + LabelHooks},
		{
			name: "unknown event",
			raw:  `[{"name":"a","events":{"boot":{"args":["true"]}}}]`,
			want: `unknown lifecycle event "boot"`,
		},
		{
			name: "duplicate name",
			raw: `[{"name":"a","events":{"start_pre":{"args":["true"]}}},` +
				`{"name":"a","events":{"stop_post":{"args":["true"]}}}]`,
			want: "used by more than one hook",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeHooksLabel(tc.raw)
			assert.Assert(t, cmp.ErrorContains(err, tc.want))
		})
	}
}
