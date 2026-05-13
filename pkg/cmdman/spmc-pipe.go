package cmdman

import "sync"

// spmcPipe is single producer - multiple consumer pipe.
type spmcPipe[T any] struct {
	mu          sync.Mutex
	subscribers map[int]chan T
	nextID      int
	closed      bool
}

func newFanout[T any]() *spmcPipe[T] {
	return &spmcPipe[T]{
		subscribers: make(map[int]chan T),
	}
}

// Subscribe adds a new subscriber and returns a channel and unsubscribe function.
func (f *spmcPipe[T]) Subscribe() (<-chan T, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := f.nextID
	f.nextID++
	ch := make(chan T, 64)
	if f.closed {
		close(ch)
		return ch, func() {}
	}
	f.subscribers[id] = ch

	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.subscribers[id]; !ok {
			return
		}
		close(ch)
		delete(f.subscribers, id)
	}
}

// Send sends data to all subscribers. Non-blocking: drops data for slow subscribers.
func (f *spmcPipe[T]) Send(data T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}

	for _, ch := range f.subscribers {
		select {
		case ch <- data:
		default:
			// Drop for slow subscriber.
		}
	}
}

func (f *spmcPipe[T]) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.closed = true
	for id, ch := range f.subscribers {
		close(ch)
		delete(f.subscribers, id)
	}
}
