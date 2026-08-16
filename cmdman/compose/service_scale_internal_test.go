package compose

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func scaleTestSpec() ComposeSpec {
	return ComposeSpec{
		Project: "proj",
		WorkDir: "/work",
		Commands: []Command{
			{Name: "web", Scale: 3},
			{Name: "db"},
		},
	}
}

// The override is what makes the reconcile a scale: it lands on the named
// command only, leaving the rest of the spec at the file's counts.
func TestApplyScaleOverrides(t *testing.T) {
	spec := scaleTestSpec()
	assert.NilError(t, applyScaleOverrides(&spec, map[string]int{"web": 1}))
	assert.Equal(t, spec.Commands[0].Scale, 1)
	assert.Equal(t, spec.Commands[1].Scale, 0)
}

func TestApplyScaleOverridesUnknownCommand(t *testing.T) {
	spec := scaleTestSpec()
	err := applyScaleOverrides(&spec, map[string]int{"nope": 2})
	assert.ErrorContains(t, err, `unknown compose command "nope"`)
}

// A programmatic caller (the TUI's minus key) can ask for zero, which Plan
// would otherwise read as an unscaled single instance rather than as the
// nonsense it is.
func TestApplyScaleOverridesRejectsBelowOne(t *testing.T) {
	spec := scaleTestSpec()
	err := applyScaleOverrides(&spec, map[string]int{"web": 0})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "must be >= 1"))
	assert.Equal(t, spec.Commands[0].Scale, 3, "a rejected count must not reach the spec")
}
