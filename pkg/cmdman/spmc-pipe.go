package cmdman

import "sync"

// spmcPipe is single producer - multiple consumer pipe
type spmcPipe struct {
	mu          sync.Mutex
	subscribers map[int]chan []byte
	nextID      int
}

func newFanout() *spmcPipe {
	return &spmcPipe{
		subscribers: make(map[int]chan []byte),
	}
}

// Subscribe adds a new subscriber and returns a channel and unsubscribe function.
func (f *spmcPipe) Subscribe() (<-chan []byte, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := f.nextID
	f.nextID++
	ch := make(chan []byte, 64)
	f.subscribers[id] = ch

	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.subscribers, id)
		close(ch)
	}
}

// Send sends data to all subscribers. Non-blocking: drops data for slow subscribers.
func (f *spmcPipe) Send(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Copy data so each subscriber gets an independent slice.
	buf := make([]byte, len(data))
	copy(buf, data)

	for _, ch := range f.subscribers {
		select {
		case ch <- buf:
		default:
			// Drop for slow subscriber.
		}
	}
}
