package cmdman

import "sync"

// ringBuffer is a thread-safe byte ring buffer for scrollback.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	pos  int
	full bool
}

// newRingBuffer creates a ring buffer with the given capacity in bytes.
func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, capacity)}
}

// Write appends data to the ring buffer, overwriting the oldest data if full.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	pLen := len(p)
	bufLen := len(r.buf)

	if pLen >= bufLen {
		// Data is larger than buffer; keep only the last cap bytes.
		copy(r.buf, p[pLen-bufLen:])
		r.pos = 0
		r.full = true
		return pLen, nil
	}

	// Write data, wrapping around.
	remaining := bufLen - r.pos
	if remaining >= pLen {
		copy(r.buf[r.pos:], p)
	} else {
		copy(r.buf[r.pos:], p[:remaining])
		copy(r.buf, p[remaining:])
	}

	r.pos = (r.pos + pLen) % bufLen
	if !r.full && r.pos < pLen {
		// We've wrapped around at least once during this write.
		r.full = true
	}

	return pLen, nil
}

// Bytes returns the current contents of the ring buffer in order.
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]byte, len(r.buf))
	n := copy(out, r.buf[r.pos:])
	copy(out[n:], r.buf[:r.pos])
	return out
}
