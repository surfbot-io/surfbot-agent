package intervalsched

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTargetLocks_MutualExclusion(t *testing.T) {
	t.Parallel()
	idx := NewTargetLockIndex()

	const goroutines = 1000
	var success int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if idx.TryAcquire("t1") {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("expected exactly 1 successful acquire, got %d", success)
	}
	if !idx.IsHeld("t1") {
		t.Fatalf("expected lock to be held after single successful acquire")
	}

	idx.Release("t1")
	if idx.IsHeld("t1") {
		t.Fatalf("expected lock released")
	}
	if !idx.TryAcquire("t1") {
		t.Fatalf("expected acquire after release to succeed")
	}
}

func TestTargetLocks_PerTarget(t *testing.T) {
	t.Parallel()
	idx := NewTargetLockIndex()

	const targets = 100
	for i := 0; i < targets; i++ {
		id := fmt.Sprintf("t%d", i)
		if !idx.TryAcquire(id) {
			t.Fatalf("expected to acquire %s on fresh index", id)
		}
	}
	for i := 0; i < targets; i++ {
		id := fmt.Sprintf("t%d", i)
		if !idx.IsHeld(id) {
			t.Fatalf("expected %s held", id)
		}
	}
	// Second acquire on any target must fail while first is held.
	for i := 0; i < targets; i++ {
		id := fmt.Sprintf("t%d", i)
		if idx.TryAcquire(id) {
			t.Fatalf("expected second acquire on %s to fail", id)
		}
	}
}

func TestTargetLocks_ReleaseWithoutAcquire(t *testing.T) {
	t.Parallel()
	idx := NewTargetLockIndex()

	idx.Release("nonexistent")
	idx.Release("nonexistent")

	if idx.IsHeld("nonexistent") {
		t.Fatalf("expected fresh lock to be not held")
	}
	if !idx.TryAcquire("nonexistent") {
		t.Fatalf("expected acquire to succeed after spurious releases")
	}
}

func TestTargetLocks_IsHeld(t *testing.T) {
	t.Parallel()
	idx := NewTargetLockIndex()

	if idx.IsHeld("t") {
		t.Fatalf("fresh lock reports held")
	}
	if !idx.TryAcquire("t") {
		t.Fatalf("first acquire failed")
	}
	if !idx.IsHeld("t") {
		t.Fatalf("after acquire, IsHeld should be true")
	}
	idx.Release("t")
	if idx.IsHeld("t") {
		t.Fatalf("after release, IsHeld should be false")
	}
	if !idx.TryAcquire("t") {
		t.Fatalf("reacquire after release failed")
	}
	if !idx.IsHeld("t") {
		t.Fatalf("after reacquire, IsHeld should be true")
	}
}

func TestTargetLocks_ConcurrentAcquireRelease(t *testing.T) {
	t.Parallel()
	idx := NewTargetLockIndex()

	const workers = 64
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(id string) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if idx.TryAcquire(id) {
					idx.Release(id)
				}
			}
		}(fmt.Sprintf("t%d", w%8))
	}
	wg.Wait()
}
