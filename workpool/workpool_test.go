package workpool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	// Test when 0 workers are passed, it should default to 1
	wp := New(0)
	if wp.workers != 1 {
		t.Fatalf("expected workers to be 1, got %d", wp.workers)
	}

	// Test when workers > 0 are passed
	wp = New(5)
	if wp.workers != 5 {
		t.Fatalf("expected workers to be 5, got %d", wp.workers)
	}
}

func TestWorkPool_Do(t *testing.T) {
	wp := New(3)

	initialQueueLength := len(wp.queue)
	wp.Do(func() {})
	newQueueLength := len(wp.queue)

	if newQueueLength != initialQueueLength+1 {
		t.Fatalf("expected queue length to increase by 1, but got %d", newQueueLength-initialQueueLength)
	}
}

func TestWorkPool_Run(t *testing.T) {
	wp := New(2)

	var counter int32
	// Add tasks to increment counter
	wp.Do(func() {
		atomic.AddInt32(&counter, 1)
	})
	wp.Do(func() {
		atomic.AddInt32(&counter, 1)
	})
	wp.Do(func() {
		atomic.AddInt32(&counter, 1)
	})

	// Run the workpool and wait for tasks to complete
	wp.Run()

	if counter != 3 {
		t.Fatalf("expected counter to be 3, but got %d", counter)
	}
}

func TestWorkPool_RunWithMoreTasksThanWorkers(t *testing.T) {
	wp := New(2)

	var counter int32
	// Add 5 tasks, but only 2 workers
	for i := 0; i < 5; i++ {
		wp.Do(func() {
			atomic.AddInt32(&counter, 1)
		})
	}

	// Run the workpool and wait for tasks to complete
	wp.Run()

	if counter != 5 {
		t.Fatalf("expected counter to be 5, but got %d", counter)
	}
}

func TestWorkPool_RunNoTasks(_ *testing.T) {
	wp := New(3)

	// Running without any tasks should complete without issues
	wp.Run()
	// If this test completes without deadlocking, it passes
}

func TestWorkPool_PanicRecovery(t *testing.T) {
	wp := New(2)

	var counter int32
	// Add a task that panics
	wp.Do(func() {
		panic("simulated panic")
	})
	// Add another task to increment the counter
	wp.Do(func() {
		atomic.AddInt32(&counter, 1)
	})

	// Run the workpool and wait for tasks to complete
	wp.Run()

	if counter != 1 {
		t.Fatalf("expected counter to be 1 after panic, but got %d", counter)
	}
}

func TestWorkPool_ConcurrentTasks(t *testing.T) {
	wp := New(10)

	var counter int32
	// Add 100 tasks, to simulate high concurrency
	for i := 0; i < 100; i++ {
		wp.Do(func() {
			atomic.AddInt32(&counter, 1)
		})
	}

	// Run the workpool and wait for tasks to complete
	wp.Run()

	if counter != 100 {
		t.Fatalf("expected counter to be 100, but got %d", counter)
	}
}

func TestWorkPool_TaskOrder(t *testing.T) {
	wp := New(2)

	var order []int
	var mu sync.Mutex

	// Add tasks that append to the order slice
	for i := 1; i <= 5; i++ {
		val := i
		wp.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, val)
		})
	}

	wp.Run()

	if len(order) != 5 {
		t.Fatalf("expected 5 tasks to be executed, but got %d", len(order))
	}

	// Ensure all values from 1 to 5 are in order (since we're using 2 workers, order may not be strict)
	expected := []int{1, 2, 3, 4, 5}
	mu.Lock()
	for i, v := range order {
		if v != expected[i] {
			t.Errorf("task %d executed out of expected order, got %d", i+1, v)
		}
	}
	mu.Unlock()
}

func TestWorkPool_LongRunningTasks(t *testing.T) {
	wp := New(3)

	var counter int32

	// Add long-running tasks (simulate 100ms delay)
	for i := 0; i < 30; i++ {
		wp.Do(func() {
			time.Sleep(100 * time.Millisecond)
			atomic.AddInt32(&counter, 1)
		})
	}

	start := time.Now()

	wp.Run()

	elapsed := time.Since(start)

	if counter != 30 {
		t.Fatalf("expected counter to be 300, but got %d", counter)
	}

	// Ensure all tasks complete within a reasonable timeframe
	if elapsed > 2*time.Second {
		t.Fatalf("tasks took too long to complete, elapsed time: %v", elapsed)
	}
}
