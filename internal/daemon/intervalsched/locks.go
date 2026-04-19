package intervalsched

import "sync"

// TargetLockIndex provides non-blocking, per-target mutual exclusion. The
// master ticker uses it to guarantee that a target has at most one scan
// in flight at a time without blocking the dispatch loop.
//
// Each target's lock is a buffered(1) channel kept in a sync.Map. Acquire
// is a non-blocking send; release is a non-blocking receive. The channel
// itself is created lazily on first touch.
type TargetLockIndex struct {
	m sync.Map // map[string]chan struct{}
}

// NewTargetLockIndex returns an empty lock index.
func NewTargetLockIndex() *TargetLockIndex {
	return &TargetLockIndex{}
}

// TryAcquire attempts to take the lock for targetID. Returns true on
// success, false if already held. Never blocks.
func (i *TargetLockIndex) TryAcquire(targetID string) bool {
	ch := i.channelFor(targetID)
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release drops the lock for targetID. A release without a matching
// acquire is a no-op (it does not panic).
func (i *TargetLockIndex) Release(targetID string) {
	ch := i.channelFor(targetID)
	select {
	case <-ch:
	default:
	}
}

// IsHeld reports whether targetID's lock is currently held. The result is
// a snapshot — by the time the caller reads it the state may already
// have changed. Intended for debug/introspection only.
func (i *TargetLockIndex) IsHeld(targetID string) bool {
	ch := i.channelFor(targetID)
	return len(ch) > 0
}

func (i *TargetLockIndex) channelFor(targetID string) chan struct{} {
	if v, ok := i.m.Load(targetID); ok {
		return v.(chan struct{})
	}
	ch := make(chan struct{}, 1)
	actual, _ := i.m.LoadOrStore(targetID, ch)
	return actual.(chan struct{})
}
