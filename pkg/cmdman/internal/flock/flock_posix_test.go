//go:build !plan9 && !windows && !wasm

package flock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	f1, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()

	// An unheld lock is acquired.
	acquired, err := TryLockExclusive(f1)
	if err != nil {
		t.Fatalf("TryLockExclusive f1: %v", err)
	}
	if !acquired {
		t.Fatal("TryLockExclusive f1: expected to acquire an unheld lock")
	}

	// An independent fd cannot take the busy lock; contention is reported as
	// acquired=false with a nil error, not as an error.
	f2, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	acquired, err = TryLockExclusive(f2)
	if err != nil {
		t.Fatalf("TryLockExclusive f2 (busy): %v", err)
	}
	if acquired {
		t.Fatal("TryLockExclusive f2: acquired a lock already held by f1")
	}

	// Once f1 releases, f2 can take it.
	if err := Unlock(f1); err != nil {
		t.Fatalf("Unlock f1: %v", err)
	}
	acquired, err = TryLockExclusive(f2)
	if err != nil {
		t.Fatalf("TryLockExclusive f2 (released): %v", err)
	}
	if !acquired {
		t.Fatal("TryLockExclusive f2: expected to acquire a released lock")
	}
}
