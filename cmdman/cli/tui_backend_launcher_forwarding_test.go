package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/cmdman/tui"
)

type launcherComposeRecorder struct {
	muxUpOptions   []compose.MuxUpOption
	muxLandOptions []compose.MuxLandOption
}

func (*launcherComposeRecorder) Up(
	context.Context,
	compose.ComposeSpec,
	compose.UpOption,
) (*compose.UpResult, error) {
	return &compose.UpResult{CreateResult: compose.CreateResult{
		Actions: []compose.ActionOutcome{{Action: string(compose.ActionCreate)}},
	}}, nil
}

func (r *launcherComposeRecorder) MuxUp(
	_ context.Context,
	opts compose.MuxUpOption,
) error {
	r.muxUpOptions = append(r.muxUpOptions, opts)
	return nil
}

func (r *launcherComposeRecorder) MuxLand(
	_ context.Context,
	opts compose.MuxLandOption,
) (mux.LandResult, error) {
	r.muxLandOptions = append(r.muxLandOptions, opts)
	return mux.LandResult{}, nil
}

func TestLauncherActionsForwardWindowName(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "launcher-action")
	composeFile := filepath.Join(workDir, "cmd-compose.yaml")
	writeComposeFile(t, composeFile, muxComposeYAML)
	target := tui.LaunchTarget{WorkDir: workDir, Project: "tools", File: composeFile}
	const want = "launcher-…"

	t.Run("start forwards mux up option", func(t *testing.T) {
		recorder := &launcherComposeRecorder{}
		if err := (&serviceBackend{}).startProject(t.Context(), target, recorder); err != nil {
			t.Fatal(err)
		}
		if len(recorder.muxUpOptions) != 1 {
			t.Fatalf("MuxUp calls = %d, want 1", len(recorder.muxUpOptions))
		}
		if got := recorder.muxUpOptions[0].WindowName; got != want {
			t.Errorf("MuxUp WindowName = %q, want %q", got, want)
		}
		if len(recorder.muxLandOptions) != 0 {
			t.Fatalf("MuxLand calls = %d, want 0", len(recorder.muxLandOptions))
		}
	})

	t.Run("launch forwards the same name to up and land", func(t *testing.T) {
		recorder := &launcherComposeRecorder{}
		if _, err := (&serviceBackend{}).launchProject(t.Context(), target, recorder); err != nil {
			t.Fatal(err)
		}
		if len(recorder.muxUpOptions) != 1 || len(recorder.muxLandOptions) != 1 {
			t.Fatalf("MuxUp calls = %d, MuxLand calls = %d, want 1 each",
				len(recorder.muxUpOptions), len(recorder.muxLandOptions))
		}
		if got := recorder.muxUpOptions[0].WindowName; got != want {
			t.Errorf("MuxUp WindowName = %q, want %q", got, want)
		}
		if got := recorder.muxLandOptions[0].WindowName; got != want {
			t.Errorf("MuxLand WindowName = %q, want %q", got, want)
		}
	})
}
