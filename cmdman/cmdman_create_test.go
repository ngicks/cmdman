package cmdman

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/model"
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

func TestCreateStoresStopSettings(t *testing.T) {
	svc := NewService(testConfig(t, t.TempDir()))
	defer svc.Close()

	res, err := svc.Create(t.Context(), CreateRequest{
		Name:        "with-stop",
		Argv:        []string{"true"},
		StopTimeout: 30 * time.Second,
		StopCommand: []string{"kill", "-QUIT", "1"},
	})
	assert.NilError(t, err)
	out, err := svc.Inspect(t.Context(), res.ID)
	assert.NilError(t, err)
	assert.Assert(t, out.Config.StopTimeout != nil)
	assert.Equal(t, time.Duration(*out.Config.StopTimeout), 30*time.Second)
	assert.DeepEqual(t, out.Config.StopCommand, &model.StopCommand{
		Args: []string{"kill", "-QUIT", "1"},
	})

	res, err = svc.Create(t.Context(), CreateRequest{Name: "without-stop", Argv: []string{"true"}})
	assert.NilError(t, err)
	out, err = svc.Inspect(t.Context(), res.ID)
	assert.NilError(t, err)
	assert.Assert(t, out.Config.StopTimeout == nil)
	assert.Assert(t, out.Config.StopCommand == nil)
}

func TestCreateRejectsNegativeStopTimeout(t *testing.T) {
	svc := NewService(testConfig(t, t.TempDir()))
	defer svc.Close()

	_, err := svc.Create(t.Context(), CreateRequest{
		Argv:        []string{"true"},
		StopTimeout: -time.Second,
	})

	assert.ErrorContains(t, err, "stop_timeout must be positive")
}
