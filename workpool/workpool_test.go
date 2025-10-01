package workpool

import (
	"fmt"
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

func TestWorkPoolBasic(t *testing.T) {
	wp := New(2) // Create a work pool with 2 workers

	var count int32
	task := func() {
		atomic.AddInt32(&count, 1)
	}

	wp.Do(task).Do(task).Do(task)
	wp.Run()

	if count != 3 {
		t.Errorf("expected 3 tasks to complete, got %d", count)
	}
}

func TestWorkPoolConcurrency(t *testing.T) {
	wp := New(3) // Create a work pool with 3 workers

	var count int32
	task := func() {
		time.Sleep(100 * time.Millisecond) // Simulate work
		atomic.AddInt32(&count, 1)
	}

	start := time.Now()

	// Add 3 tasks
	wp.Do(task).Do(task).Do(task)

	wp.Run()

	duration := time.Since(start)

	if count != 3 {
		t.Errorf("expected 3 tasks to complete, got %d", count)
	}

	// Since there are 3 workers, all tasks should complete in ~100ms
	if duration > 150*time.Millisecond {
		t.Errorf("expected tasks to complete in ~100ms, took %s", duration)
	}
}

func TestWorkPoolWithMoreTasksThanWorkers(t *testing.T) {
	wp := New(2)

	var counter int32
	// Add 5 tasks, but only 2 workers
	for range 5 {
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

func TestWorkPoolNoTasks(t *testing.T) {
	now := time.Now()
	wp := New(3)

	// Run with no tasks, should return immediately
	wp.Run()

	if time.Since(now) > 10*time.Millisecond {
		t.Fatalf("Expected Run() to return immediately with no tasks")
	}
}

func TestWorkPoolPanicSafety(t *testing.T) {
	wp := New(2)

	var count int32
	task := func() {
		defer atomic.AddInt32(&count, 1)
		panic("intentional panic")
	}

	wp.Do(task).Do(task).Do(task)
	wp.Run()

	if count != 3 {
		t.Errorf("expected 3 tasks to complete even with panics, got %d", count)
	}
}

func TestWorkPoolAddAfterRun(t *testing.T) {
	wp := New(1)

	var count int32
	task := func() {
		atomic.AddInt32(&count, 1)
	}

	wp.Do(task).Do(task)
	wp.Run()

	// Try adding another task after Run()
	wp.Do(task)

	if count != 2 {
		t.Errorf("expected 2 tasks to complete, got %d", count)
	}
}

func BenchmarkWorkPool(b *testing.B) {
	workerScenarios := []int{1, 5, 10, 50, 100, 500, 1000}
	for _, workers := range workerScenarios {
		name := fmt.Sprintf("%d", workers)
		b.Run(name, func(b *testing.B) {
			var completedTasks int32
			wp := New(workers)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wp.Do(func() {
					atomic.AddInt32(&completedTasks, 1)
				})
			}
			wp.Run()

			if int(completedTasks) != b.N {
				b.Errorf("Expected %d completed tasks, got %d", b.N, completedTasks)
			}
		})
	}
}
