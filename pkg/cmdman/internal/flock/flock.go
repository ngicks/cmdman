// Package flock wraps advisory file locks (flock(2) on POSIX). On platforms
// without flock support the exclusive helpers return an error and Unlock is a
// no-op so callers can fall back without build tags.
package flock

import "os"

// TryLockExclusive attempts to acquire an exclusive advisory lock on f without
// blocking. acquired reports whether the lock was taken; when it is false with
// a nil error the lock is currently held by another fd (lock contention). A
// non-nil error signals an unexpected failure that is not contention and must
// not be treated as "lock available".
func TryLockExclusive(f *os.File) (acquired bool, err error) {
	return tryLockExclusive(f)
}

// LockExclusive acquires an exclusive advisory lock on f, blocking until the
// lock is available.
func LockExclusive(f *os.File) error {
	return lockExclusive(f)
}

// LockShared acquires a shared advisory lock on f, blocking until the lock
// is available. Multiple holders of a shared lock can coexist; exclusive
// holders block all others.
func LockShared(f *os.File) error {
	return lockShared(f)
}

// Unlock releases an advisory lock held on f.
func Unlock(f *os.File) error {
	return unlock(f)
}
