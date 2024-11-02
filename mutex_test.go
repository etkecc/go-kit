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

	for i := 0; i < numGoroutines; i++ {
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

func TestMutex_LockUnlockMultipleKeys(t *testing.T) {
	km := NewMutex()
	keys := []string{"key1", "key2", "key3"}
	var wg sync.WaitGroup
	const numGoroutines = 3

	// Map to store counts for each key
	results := make(map[string]int)
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
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

func BenchmarkSyncMutex(b *testing.B) {
	var mu sync.Mutex
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mu.Lock()
		mu.Unlock() //nolint:gocritic,staticcheck // that's the point
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
			mu.Unlock() //nolint:gocritic,staticcheck // that's the point
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
