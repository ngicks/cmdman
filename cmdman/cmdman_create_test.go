package cmdman

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestCreateReplaceRequiresName(t *testing.T) {
	svc := NewService(testConfig(t, t.TempDir()))
	defer svc.Close()

	_, err := svc.Create(t.Context(), CreateRequest{
		Argv:    []string{"true"},
		Replace: true,
	})

	assert.ErrorContains(t, err, "replace requires a name")
}
