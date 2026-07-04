package muxctl_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ngicks/cmdman/pkg/muxctl"
)

// nameSeq makes every registered driver name unique within a single test binary
// process. The registry is write-once (a duplicate RegisterDriver panics), so a
// fixed literal name would panic under `go test -count=N` — t.Name() alone is
// identical across -count reruns in the same process, so a monotonic suffix is
// required on top of it.
var nameSeq atomic.Uint64

// uniqueName derives a registry name unique per registration, even across
// -count=N reruns of the same test in one process.
func uniqueName(t *testing.T) string {
	t.Helper()
	return t.Name() + "#" + strconv.FormatUint(nameSeq.Add(1), 10)
}

// stubDriver is a no-op [muxctl.Driver] used only to exercise the registry.
type stubDriver struct{}

func (stubDriver) New(context.Context, muxctl.Config) (muxctl.Session, error) {
	return nil, errors.New("stub")
}

func (stubDriver) Open(context.Context, muxctl.Config) (muxctl.Session, bool, error) {
	return nil, false, nil
}

func (stubDriver) ListWindows(
	context.Context, muxctl.ListOptions,
) ([]muxctl.Window, error) {
	return nil, nil
}

func (stubDriver) FindPane(
	context.Context, muxctl.ListOptions, string, string,
) (string, bool, error) {
	return "", false, nil
}

func (stubDriver) ReadWindowState(
	context.Context, muxctl.ListOptions, string, muxctl.StateKey,
) (string, error) {
	return "", nil
}

func (stubDriver) WriteWindowState(
	context.Context, muxctl.ListOptions, string, muxctl.StateKey, string,
) error {
	return nil
}

// TestRegisterAndLookupDriver covers the happy path: a registered driver is
// returned verbatim by LookupDriver.
func TestRegisterAndLookupDriver(t *testing.T) {
	name := uniqueName(t)
	want := stubDriver{}
	muxctl.RegisterDriver(name, want)

	got, err := muxctl.LookupDriver(name)
	if err != nil {
		t.Fatalf("LookupDriver(%q): %v", name, err)
	}
	if got != muxctl.Driver(want) {
		t.Errorf("LookupDriver(%q) = %v, want %v", name, got, want)
	}
}

// TestLookupDriver_Unknown verifies the lookup error names the missing driver.
func TestLookupDriver_Unknown(t *testing.T) {
	name := uniqueName(t) // never registered
	got, err := muxctl.LookupDriver(name)
	if err == nil {
		t.Fatalf("LookupDriver(%q) = %v, want error", name, got)
	}
	if got != nil {
		t.Errorf("LookupDriver(%q) driver = %v, want nil", name, got)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error %q does not name the missing driver %q", err, name)
	}
}

// TestRegisterDriver_NilPanics verifies RegisterDriver panics on a nil driver,
// with a message identifying the nil-driver programming error.
func TestRegisterDriver_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("RegisterDriver(nil) did not panic")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "nil") {
			t.Errorf("panic message %q does not mention the nil driver", r)
		}
	}()
	muxctl.RegisterDriver(uniqueName(t), nil)
}

// TestRegisterDriver_DuplicatePanics verifies a second RegisterDriver under the
// same name panics (rather than silently overwriting) with a message naming the
// duplicated driver.
func TestRegisterDriver_DuplicatePanics(t *testing.T) {
	name := uniqueName(t)
	muxctl.RegisterDriver(name, stubDriver{})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate RegisterDriver did not panic")
		}
		if msg, _ := r.(string); !strings.Contains(msg, name) {
			t.Errorf("panic message %q does not name the duplicated driver %q", r, name)
		}
	}()
	muxctl.RegisterDriver(name, stubDriver{})
}
