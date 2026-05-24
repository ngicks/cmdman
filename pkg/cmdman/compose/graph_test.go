package compose_test

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// ---- dependency cycle rejection ---------------------------------------------

func TestCycleRejection(t *testing.T) {
	_, err := normalizeFromFile(t, testdataPath("cycle.yaml"), compose.NormalizeOpts{})
	assert.Assert(t, err != nil, "expected error for dependency cycle")
	assert.Assert(t, cmp.Contains(err.Error(), "cycle"))
}

func TestNoCycleValidSpec(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)
	err = compose.ValidateDAG(spec.Commands)
	assert.NilError(t, err)
}

// ---- topological layering ---------------------------------------------------

func TestTopoLayers_LinearChain(t *testing.T) {
	// a → b → c (c depends on b which depends on a)
	commands := []compose.Command{
		{Name: "a"},
		{Name: "b", After: []compose.AfterSpec{{Name: "a", Condition: compose.ConditionCompleted}}},
		{Name: "c", After: []compose.AfterSpec{{Name: "b", Condition: compose.ConditionCompleted}}},
	}
	layers, err := compose.TopoLayers(commands)
	assert.NilError(t, err)
	assert.Equal(t, len(layers), 3)
	assert.DeepEqual(t, layers[0], []string{"a"})
	assert.DeepEqual(t, layers[1], []string{"b"})
	assert.DeepEqual(t, layers[2], []string{"c"})
}

func TestTopoLayers_Parallel(t *testing.T) {
	// a and b independent; c depends on both
	commands := []compose.Command{
		{Name: "a"},
		{Name: "b"},
		{Name: "c", After: []compose.AfterSpec{
			{Name: "a", Condition: compose.ConditionCompleted},
			{Name: "b", Condition: compose.ConditionCompleted},
		}},
	}
	layers, err := compose.TopoLayers(commands)
	assert.NilError(t, err)
	assert.Equal(t, len(layers), 2)
	assert.DeepEqual(t, layers[0], []string{"a", "b"})
	assert.DeepEqual(t, layers[1], []string{"c"})
}
