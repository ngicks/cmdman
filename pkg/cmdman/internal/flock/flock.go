// Package flock wraps advisory file locks.
package flock

import "os"

// TryLockExclusive attempts a non-blocking exclusive lock. A false, nil result
// means another file descriptor holds the lock.
func TryLockExclusive(f *os.File) (acquired bool, err error) {
	return tryLockExclusive(f)
}

// LockExclusive blocks until it acquires an exclusive advisory lock on f.
func LockExclusive(f *os.File) error {
	return lockExclusive(f)
}

// LockShared blocks until it acquires a shared advisory lock on f.
func LockShared(f *os.File) error {
	return lockShared(f)
}

// Unlock releases an advisory lock held on f.
func Unlock(f *os.File) error {
	return unlock(f)
}
