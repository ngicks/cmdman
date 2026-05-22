// Package flock wraps advisory file locks (flock(2) on POSIX). On platforms
// without flock support the exclusive helpers return an error and Unlock is a
// no-op so callers can fall back without build tags.
package flock

import "os"

// TryLockExclusive acquires an exclusive advisory lock on f without blocking.
// It returns an error if the lock is already held by another fd.
func TryLockExclusive(f *os.File) error {
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
