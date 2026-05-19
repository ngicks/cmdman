//go:build linux

package eventlog

import "unsafe"

// ptrAt returns an unsafe.Pointer to the n-th byte of b. It is used to
// reinterpret inotify-read bytes as unix.InotifyEvent without copying.
func ptrAt(b []byte, n int) unsafe.Pointer {
	return unsafe.Pointer(&b[n])
}
