package kit

import (
	"sync"
	"testing"
	"time"
)

func TestMutex_LockUnlock(_ *testing.T) {
	km := NewMutex()
	key := "testKey"

	// Ensure Lock and Unlock do not cause a deadlock or panic
	km.Lock(key)
	km.Unlock(key)
}

func TestMutex_LockTwice(t *testing.T) {
	km := NewMutex()
	key := "testKey"

	km.Lock(key)

	locked := make(chan struct{})

	// Try to lock the same key in a new goroutine; this should block until we unlock it
	go func() {
		km.Lock(key)
		close(locked)
	}()

	// Ensure the goroutine is blocked
	time.Sleep(50 * time.Millisecond)
	select {
	case <-locked:
		t.Error("Expected lock to block, but it was acquired twice for the same key")
	default:
		// Success: lock is held, and the second goroutine is blocked
	}

	// Unlock and ensure the second goroutine can proceed
	km.Unlock(key)

	select {
	case <-locked:
		// Success: second goroutine acquired the lock after unlock
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected the second goroutine to acquire the lock after unlock")
	}
}

func TestMutex_ConcurrentAccess(t *testing.T) {
	km := NewMutex()
	key := "testKey"
	var wg sync.WaitGroup
	const numGoroutines = 10

	// Counter to check synchronized access
	counter := 0

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			km.Lock(key)
			defer km.Unlock(key)

			// Increment counter safely
			temp := counter
			time.Sleep(10 * time.Millisecond) // simulate work
			counter = temp + 1
		}()
	}

	wg.Wait()

	// Counter should equal the number of goroutines if locking worked
	if counter != numGoroutines {
		t.Errorf("Expected counter to be %d, got %d", numGoroutines, counter)
	}
}

func TestMutex_UnlockWithoutLock(_ *testing.T) {
	km := NewMutex()
	key := "nonExistentKey"

	// Unlock a key that was never locked; it should not panic or cause errors
	km.Unlock(key)
}

func TestMutex_KeyCleanup(t *testing.T) {
	km := NewMutex()

	// Lock and unlock several keys; the internal map must be empty afterwards.
	for _, key := range []string{"a", "b", "c"} {
		km.Lock(key)
		km.Unlock(key)
	}

	km.mu.Lock() //nolint:SA2001 // reading map len under lock
	n := len(km.locks)
	km.mu.Unlock()

	if n != 0 {
		t.Fatalf("expected 0 entries in locks map after all keys released, got %d", n)
	}
}

func TestMutex_KeyCleanup_Concurrent(t *testing.T) {
	km := NewMutex()
	var wg sync.WaitGroup
	const goroutines = 50

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "shared"
			if i%2 == 0 {
				key = "other"
			}
			km.Lock(key)
			km.Unlock(key)
		}(i)
	}
	wg.Wait()

	km.mu.Lock() //nolint:SA2001 // reading map len under lock
	n := len(km.locks)
	km.mu.Unlock()

	if n != 0 {
		t.Fatalf("expected 0 entries in locks map after all goroutines finished, got %d", n)
	}
}

func TestMutex_LockUnlockMultipleKeys(t *testing.T) {
	km := NewMutex()
	keys := []string{"key1", "key2", "key3"}
	var wg sync.WaitGroup
	const numGoroutines = 3

	// Map to store counts for each key
	results := make(map[string]int)
	var mu sync.Mutex

	for range numGoroutines {
		for _, key := range keys {
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				km.Lock(key)
				defer km.Unlock(key)

				// Increment result for this key
				mu.Lock()
				results[key]++
				mu.Unlock()
			}(key)
		}
	}

	wg.Wait()

	// Verify that each key's result matches the number of goroutines
	for _, key := range keys {
		if results[key] != numGoroutines {
			t.Errorf("Expected result for key %s to be %d, got %d", key, numGoroutines, results[key])
		}
	}
}

func TestMutex_ReuseAfterCleanup(t *testing.T) {
	km := NewMutex()
	key := "reuse"

	// First use: lock and unlock (entry should be deleted on unlock).
	km.Lock(key)
	km.Unlock(key)

	km.mu.Lock() //nolint:SA2001 // reading map len under lock — not an empty section
	n := len(km.locks)
	km.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 entries after first unlock, got %d", n)
	}

	// Second use: the key must be re-creatable and work correctly.
	done := make(chan struct{})
	km.Lock(key)
	go func() {
		km.Lock(key)
		close(done)
		km.Unlock(key)
	}()

	// Goroutine must block while we hold the lock.
	select {
	case <-done:
		t.Fatal("goroutine acquired lock before it was released")
	default:
	}

	km.Unlock(key)

	select {
	case <-done:
		// success: goroutine got the lock after we released it
	case <-time.After(100 * time.Millisecond):
		t.Fatal("goroutine did not acquire lock after unlock")
	}
}

func TestMutex_UnlockAfterCleanup_NoOp(t *testing.T) {
	km := NewMutex()
	key := "cleanup-noop"

	// Lock and unlock causes refcount to reach zero, deleting the entry.
	km.Lock(key)
	km.Unlock(key)

	// A second Unlock on the same key must be a no-op (not panic).
	km.Unlock(key)

	km.mu.Lock() //nolint:SA2001 // reading map len under lock
	n := len(km.locks)
	km.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 entries, got %d", n)
	}
}

func TestMutex_MultipleWaiters_RefcountIntegrity(t *testing.T) {
	km := NewMutex()
	key := "multi-wait"
	const waiters = 5

	// Acquire the lock so all waiters block.
	km.Lock(key)

	ready := make(chan struct{}, waiters)
	done := make(chan struct{}, waiters)
	for range waiters {
		go func() {
			ready <- struct{}{}
			km.Lock(key)
			done <- struct{}{}
			km.Unlock(key)
		}()
	}

	// Wait for all goroutines to start and register their refcount.
	for range waiters {
		<-ready
	}
	time.Sleep(20 * time.Millisecond) // let them block on m.mu.Lock

	// Verify the entry exists and refcount reflects holder + waiters.
	km.mu.Lock() //nolint:SA2001 // reading map fields under lock — not an empty section
	m, exists := km.locks[key]
	var rc int
	if exists {
		rc = m.refcount
	}
	km.mu.Unlock()

	if !exists {
		t.Fatal("entry must exist while lock is held and goroutines are waiting")
	}
	if rc != waiters+1 { // +1 for the holder
		t.Fatalf("expected refcount %d (holder + %d waiters), got %d", waiters+1, waiters, rc)
	}

	// Release; all waiters should drain and the entry must be gone afterwards.
	km.Unlock(key)

	for range waiters {
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timed out waiting for a goroutine to finish")
		}
	}

	km.mu.Lock() //nolint:SA2001 // reading map len under lock
	n := len(km.locks)
	km.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 entries after all goroutines finished, got %d", n)
	}
}

func BenchmarkSyncMutex(b *testing.B) {
	var mu sync.Mutex
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mu.Lock()
		mu.Unlock() //nolint:gocritic,staticcheck,SA2001 // that's the point
	}
}

func BenchmarkMutexSingleKey(b *testing.B) {
	km := NewMutex()
	key := "singleKey"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		km.Lock(key)
		km.Unlock(key)
	}
}

func BenchmarkMutexMultipleKeys(b *testing.B) {
	km := NewMutex()
	keys := []string{"key1", "key2", "key3", "key4", "key5"}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := keys[i%len(keys)]
		km.Lock(key)
		km.Unlock(key)
	}
}

func BenchmarkSyncMutexParallel(b *testing.B) {
	var mu sync.Mutex
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mu.Lock()
			mu.Unlock() //nolint:gocritic,staticcheck,SA2001 // that's the point
		}
	})
}

func BenchmarkMutexSingleKeyParallel(b *testing.B) {
	km := NewMutex()
	key := "parallelKey"
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			km.Lock(key)
			km.Unlock(key)
		}
	})
}

func BenchmarkMutexMultipleKeysParallel(b *testing.B) {
	km := NewMutex()
	keys := []string{"key1", "key2", "key3", "key4", "key5"}
	numKeys := len(keys)

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := keys[i%numKeys]
			km.Lock(key)
			km.Unlock(key)
			i++
		}
	})
}
