package compose

import (
	"cmp"
	"context"
	"slices"
	"sync"
)

// failedRelease is one run of a release event that failed.
type failedRelease struct {
	// holder is the holder of the resource as it was before the run, enough to
	// run the release again. A resource that had no holder gets one built from
	// the run, with an empty value, and a holder that stores no release gets
	// the release of the run.
	holder resourceHolder
	// display is the name the progress events of the run gave the replica.
	display string
	err     error
	// onError is the on_error the failure was handled under: the hook's own,
	// turned into continue by a forced teardown, or fail for a run that was
	// cancelled, as a cancellation fails the run whatever on_error says.
	onError OnError
	// retried reports that the release ran once more after this failure and
	// failed again. err and onError are then those of that last run.
	retried bool
}

// fails reports whether the failure fails the operation that ran it.
func (f failedRelease) fails() bool {
	return f.onError == OnErrorFail
}

// releaseRecorder collects the failed releases of one operation. A nil
// releaseRecorder records nothing. It is safe for concurrent use.
type releaseRecorder struct {
	mu     sync.Mutex
	failed []failedRelease
}

type releaseRecorderKey struct{}

// withReleaseRecorder returns a ctx that carries rec, so every release run
// under it reports its failure to rec.
func withReleaseRecorder(ctx context.Context, rec *releaseRecorder) context.Context {
	return context.WithValue(ctx, releaseRecorderKey{}, rec)
}

// releaseRecorderFrom returns the releaseRecorder ctx carries, or nil.
func releaseRecorderFrom(ctx context.Context) *releaseRecorder {
	rec, _ := ctx.Value(releaseRecorderKey{}).(*releaseRecorder)
	return rec
}

func (r *releaseRecorder) record(f failedRelease) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, f)
}

// failures returns what r recorded, ordered by holder name. Releases run
// concurrently, so the order they were recorded in says nothing.
func (r *releaseRecorder) failures() []failedRelease {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out := slices.Clone(r.failed)
	r.mu.Unlock()
	sortFailures(out)
	return out
}

// take returns what r recorded, ordered as [releaseRecorder.failures] orders
// it, and forgets it.
func (r *releaseRecorder) take() []failedRelease {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	out := r.failed
	r.failed = nil
	r.mu.Unlock()
	sortFailures(out)
	return out
}

func sortFailures(failed []failedRelease) {
	slices.SortStableFunc(failed, func(a, b failedRelease) int {
		return cmp.Compare(a.holder.name(), b.holder.name())
	})
}
