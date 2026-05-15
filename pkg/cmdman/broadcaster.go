package cmdman

import "sync"

// broadcaster is single producer - multiple consumer broadcaster.
type broadcaster[T any] struct {
	mu          sync.Mutex
	subscribers map[int]chan T
	nextID      int
	closed      bool
}

func newBroadcaster[T any]() *broadcaster[T] {
	return &broadcaster[T]{
		subscribers: make(map[int]chan T),
	}
}

// Subscribe adds a new subscriber and returns a channel and unsubscribe function.
func (f *broadcaster[T]) Subscribe() (<-chan T, func()) {
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
func (f *broadcaster[T]) Send(data T) {
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

func (f *broadcaster[T]) Close() {
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
