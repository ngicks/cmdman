package monitor

import (
	"testing"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

// TestBroadcasterSendDeliversToAllSubscribers verifies that a value sent after
// subscription reaches every current subscriber.
func TestBroadcasterSendDeliversToAllSubscribers(t *testing.T) {
	b := newBroadcaster[int]()

	ch1, _ := b.Subscribe()
	ch2, _ := b.Subscribe()
	ch3, _ := b.Subscribe()

	b.Send(42)

	assert.Equal(t, <-ch1, 42)
	assert.Equal(t, <-ch2, 42)
	assert.Equal(t, <-ch3, 42)
}

// TestBroadcasterUnsubscribeStopsDeliveryAndLeavesOthers verifies that
// unsubscribing closes only that subscriber's channel, stops its delivery, and
// leaves other subscribers untouched. Unsubscribe is also idempotent.
func TestBroadcasterUnsubscribeStopsDeliveryAndLeavesOthers(t *testing.T) {
	b := newBroadcaster[int]()

	chA, unsubA := b.Subscribe()
	chB, _ := b.Subscribe()

	unsubA()

	b.Send(7)

	// B is undisturbed and still receives the value.
	assert.Equal(t, <-chB, 7)

	// A's channel was closed by unsubscribe; it never receives the value.
	v, ok := <-chA
	assert.Assert(t, !ok, "unsubscribed channel should be closed")
	assert.Equal(t, v, 0)

	// Unsubscribe is idempotent (must not double-close / panic).
	unsubA()
}

// TestBroadcasterCloseClosesSubscriberChannels verifies Close semantics: it
// closes every subscriber channel (buffered values remain drainable first),
// turns Send into a no-op, and is idempotent.
func TestBroadcasterCloseClosesSubscriberChannels(t *testing.T) {
	b := newBroadcaster[int]()

	chA, _ := b.Subscribe()
	chB, _ := b.Subscribe()

	b.Send(1)
	b.Close()

	// The value buffered before Close survives, then the channel reports closed.
	vA, okA := <-chA
	assert.Assert(t, okA)
	assert.Equal(t, vA, 1)
	_, okA = <-chA
	assert.Assert(t, !okA, "channel should be closed after its buffer is drained")

	vB, okB := <-chB
	assert.Assert(t, okB)
	assert.Equal(t, vB, 1)
	_, okB = <-chB
	assert.Assert(t, !okB, "channel should be closed after its buffer is drained")

	// Send after Close is a no-op; Close is idempotent.
	b.Send(2)
	b.Close()
}

// TestBroadcasterSubscribeAfterClose verifies that subscribing after Close
// returns an already-closed channel and a no-op unsubscribe.
func TestBroadcasterSubscribeAfterClose(t *testing.T) {
	b := newBroadcaster[int]()
	b.Close()

	ch, unsub := b.Subscribe()

	// The returned channel is already closed.
	_, ok := <-ch
	assert.Assert(t, !ok, "subscribe after close should return a closed channel")

	// The returned unsubscribe is a safe no-op, and Send stays a no-op.
	unsub()
	b.Send(3)
}

// TestBroadcasterDropsForSlowConsumer verifies the non-blocking send behavior:
// a subscriber that never drains does not block the sender, and messages beyond
// the channel's buffer are dropped (the earliest buffer-full values survive).
func TestBroadcasterDropsForSlowConsumer(t *testing.T) {
	b := newBroadcaster[int]()

	ch, _ := b.Subscribe()
	bufSize := cap(ch)
	assert.Assert(t, bufSize > 0, "subscriber channel should be buffered")

	// Send more than the buffer holds without draining. Every Send must return
	// (never block on the full channel).
	total := 2 * bufSize
	for i := range total {
		b.Send(i)
	}

	// Drain non-blocking: exactly one buffer's worth survived, and it is the
	// first bufSize values in order (later sends were dropped).
	var got []int
drain:
	for {
		select {
		case v := <-ch:
			got = append(got, v)
		default:
			break drain
		}
	}

	assert.Equal(t, len(got), bufSize)
	for i, v := range got {
		assert.Equal(t, v, i)
	}
}

// TestBroadcasterConcurrentStress drives Subscribe/Send/unsubscribe/Close from
// many goroutines at once. It is meaningful under -race: the shared broadcaster
// is the only synchronization point, so the race detector observes whether its
// mutex correctly guards every access. The test is deterministic in that it
// always terminates -- churners stop on the stop signal and long-lived
// subscribers are released by Close.
func TestBroadcasterConcurrentStress(t *testing.T) {
	const (
		producers   = 8
		churners    = 8
		subscribers = 8
		sends       = 500
	)

	b := newBroadcaster[int]()
	stop := make(chan struct{})

	var prodEg errgroup.Group
	for range producers {
		prodEg.Go(func() error {
			for i := range sends {
				b.Send(i)
			}
			return nil
		})
	}

	var workEg errgroup.Group

	// Churners repeatedly subscribe, drain whatever is buffered without
	// blocking, then unsubscribe -- exercising concurrent Subscribe/unsubscribe
	// against concurrent Send and Close.
	for range churners {
		workEg.Go(func() error {
			for {
				select {
				case <-stop:
					return nil
				default:
				}
				ch, unsub := b.Subscribe()
			drain:
				for {
					select {
					case _, ok := <-ch:
						if !ok {
							break drain
						}
					default:
						break drain
					}
				}
				unsub()
			}
		})
	}

	// Long-lived subscribers block until their channel is closed by Close.
	for range subscribers {
		workEg.Go(func() error {
			ch, _ := b.Subscribe()
			for range ch {
			}
			return nil
		})
	}

	// Once producers are done, stop the churners and close the broadcaster,
	// which releases the long-lived subscribers. Close runs concurrently with
	// any in-flight churner Subscribe/unsubscribe calls.
	assert.NilError(t, prodEg.Wait())
	close(stop)
	b.Close()
	assert.NilError(t, workEg.Wait())
}
