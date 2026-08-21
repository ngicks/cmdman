package monitor

import (
	"context"
	"time"
)

// defaultTitleThrottleInterval bounds how frequently title-only changes reach
// subscribers. Changes arriving within one interval are collapsed into the
// newest title, without requiring the producer to become quiet.
const defaultTitleThrottleInterval = 150 * time.Millisecond

// streamRuntimeState sends st's current state, then one message per change,
// until ctx ends or the monitor leaves an active state. A title-only change is
// held until the current throttle window elapses and then re-read, so a burst
// costs one message carrying the last title without continuous changes starving
// the stream; every other change (bell, reported status) goes out at once and
// carries the pending title with it.
//
// states may be nil, in which case only ctx ends the stream. This runs on the
// gRPC handler goroutine and touches the runtime state's own lock only - never
// outputMu, which vt callbacks hold while latching.
func streamRuntimeState(
	ctx context.Context,
	st *commandRuntimeState,
	states <-chan monitorStateChange,
	throttleInterval time.Duration,
	send func(runtimeView) error,
) error {
	// Subscribe before taking the first snapshot: a change landing between the
	// two would otherwise never be sent, since its wake predates the
	// subscription and nothing wakes us again.
	changed, unsub := st.subscribeChange()
	defer unsub()

	sent := st.snapshot().view()
	if err := send(sent); err != nil {
		return err
	}

	timer := time.NewTimer(throttleInterval)
	timer.Stop()
	defer timer.Stop()
	// fire is non-nil only while a title change waits out a throttle window.
	var fire <-chan time.Time

	flush := func() error {
		cur := st.snapshot().view()
		if cur == sent {
			return nil
		}
		sent = cur
		return send(cur)
	}

	for {
		select {
		case <-changed:
			cur := st.snapshot().view()
			if cur == sent {
				// A wake for something the view does not carry (a
				// notification), or one coalesced with a change already sent.
				continue
			}
			if titleOnlyChange(sent, cur) {
				// Arm one fixed window. Later title changes are represented by the
				// snapshot read when it fires; they must not postpone it, or a
				// continuously animated title can starve forever.
				if fire == nil {
					timer.Reset(throttleInterval)
					fire = timer.C
				}
				continue
			}
			timer.Stop()
			fire = nil
			sent = cur
			if err := send(cur); err != nil {
				return err
			}
		case <-fire:
			fire = nil
			if err := flush(); err != nil {
				return err
			}
		case state, ok := <-states:
			if ok && isMonitorActiveState(state.State) {
				continue
			}
			// The run is over; hand over whatever the throttle window still holds
			// rather than dropping the last title of the run.
			return flush()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// titleOnlyChange reports whether cur differs from prev in the title alone.
func titleOnlyChange(prev, cur runtimeView) bool {
	prev.Title = cur.Title
	return prev == cur
}
