package cli

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/compose"
	"github.com/ngicks/cmdman/cmdman/mux"
	"github.com/ngicks/cmdman/pkg/muxctl"
	"github.com/ngicks/go-common/contextkey"
)

func TestCloseProjectWindowsClosesByIdentityOnTheDeclaredDriver(t *testing.T) {
	selection := compose.ProjectSelection{
		Spec: &compose.ComposeSpec{Mux: &mux.Spec{
			Driver: muxctl.DriverSpec{Name: "tmux", Socket: "work"},
		}},
		WorkDir: "/work/tools",
		Project: "tools",
	}
	var errOut bytes.Buffer
	recorder := &muxDownRecorder{}

	closeProjectWindows(t.Context(), selection, recorder.down, &errOut)

	if len(recorder.calls) != 1 {
		t.Fatalf("mux teardown calls = %d, want 1", len(recorder.calls))
	}
	got := recorder.calls[0]
	if want := selection.ProjectIdentity(); got.Identity != want {
		t.Errorf("Identity = %q, want %q", got.Identity, want)
	}
	if !got.KillCreated {
		t.Error("the windows cmdman created must be closed, not restored")
	}
	if !reflect.DeepEqual(got.Driver, selection.Spec.Mux.Driver) {
		t.Errorf("Driver = %+v, want the declared %+v", got.Driver, selection.Spec.Mux.Driver)
	}
	if got.Stdout != io.Discard {
		t.Errorf("Stdout = %v, want io.Discard: stdout carries the down's progress", got.Stdout)
	}
	if errOut.Len() != 0 {
		t.Errorf("a teardown that worked must write nothing, got %q", errOut.String())
	}
}

func TestCloseProjectWindowsWarnsOnAFailedClose(t *testing.T) {
	var log, errOut bytes.Buffer
	ctx := contextkey.WithSlogLogger(t.Context(), slog.New(slog.NewTextHandler(&log, nil)))
	recorder := &muxDownRecorder{err: errors.New("no server running")}

	closeProjectWindows(
		ctx,
		compose.ProjectSelection{WorkDir: "/work/tools", Project: "tools"},
		recorder.down,
		&errOut,
	)

	if got := errOut.String(); !strings.HasPrefix(got, "warning: ") ||
		!strings.Contains(got, "no server running") {
		t.Errorf("errOut = %q, want a warning naming the failure", got)
	}
	if !strings.Contains(log.String(), "no server running") {
		t.Errorf("log = %q, want the failure logged too", log.String())
	}
}
