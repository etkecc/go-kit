package crontab

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// newTestCrontab builds a Crontab without starting its ticker, so tests own the clock by
// calling runDue directly. This is the seam: runDue is the single writer, and a test is a
// single goroutine, so the invariant holds.
func newTestCrontab() *Crontab {
	return &Crontab{
		loc:     time.UTC,
		onPanic: func(string, any) {},
		now:     time.Now,
		stop:    make(chan struct{}),
	}
}

// waitCount busy-polls an atomic counter up to a deadline. Used where wg.Wait can't be
// (a job is deliberately blocked), so no time.Sleep sneaks into the test.
func waitCount(t *testing.T, c *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Load() >= want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("timed out waiting for count %d, got %d", want, c.Load())
}

func TestIdempotencyBoundary(t *testing.T) {
	c := newTestCrontab()
	var fires atomic.Int32
	if err := c.AddJob("* * * * *", func() { fires.Add(1) }); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC)

	// All three land inside the 10:30 minute, stepping backward relative to each other but never
	// crossing the boundary, so the idempotency-key guard (j.last.Equal) is what's under test,
	// deterministically. A negative offset from an exact boundary would land in the PREVIOUS
	// minute, a different key, and race the overlap guard instead.
	c.runDue(base.Add(45 * time.Second))
	c.runDue(base.Add(30 * time.Second)) // backward re-tick, same minute
	c.runDue(base)                       // further backward, same minute
	c.wg.Wait()
	if got := fires.Load(); got != 1 {
		t.Fatalf("same minute should fire once, got %d", got)
	}

	c.runDue(base.Add(time.Minute)) // next minute
	c.wg.Wait()
	if got := fires.Load(); got != 2 {
		t.Fatalf("next minute should fire again, got %d", got)
	}
}

// TestIdempotencyDSTDoubleFire pins the DST behavior: under a location with daylight
// saving, the two distinct absolute instants that both read 01:30 on fall-back night are
// two distinct idempotency keys, so the job fires twice. Under UTC the same wall clock is
// one instant, so it fires once. The key is the real instant, never the wall-clock label.
func TestIdempotencyDSTDoubleFire(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// 2025-11-02: 02:00 EDT rolls back to 01:00 EST. 01:30 local happens twice:
	// once at EDT (UTC-4, so 05:30 UTC) and once at EST (UTC-5, so 06:30 UTC).
	firstInstant := time.Date(2025, 11, 2, 5, 30, 0, 0, time.UTC)
	secondInstant := time.Date(2025, 11, 2, 6, 30, 0, 0, time.UTC)
	if h, m, _ := firstInstant.In(ny).Clock(); h != 1 || m != 30 {
		t.Fatalf("setup: firstInstant reads %02d:%02d in NY, want 01:30", h, m)
	}
	if h, m, _ := secondInstant.In(ny).Clock(); h != 1 || m != 30 {
		t.Fatalf("setup: secondInstant reads %02d:%02d in NY, want 01:30", h, m)
	}

	c := newTestCrontab()
	c.loc = ny
	var fires atomic.Int32
	if err := c.AddJob("30 1 * * *", func() { fires.Add(1) }); err != nil {
		t.Fatal(err)
	}
	// The two instants are a real hour apart, so the first job finishes before the second
	// tick; wg.Wait between them isolates the idempotency key from the non-overlap guard.
	c.runDue(firstInstant)
	c.wg.Wait()
	c.runDue(secondInstant)
	c.wg.Wait()
	if got := fires.Load(); got != 2 {
		t.Fatalf("DST fall-back should double-fire, got %d", got)
	}

	// Same wall clock under UTC is a single instant: the second tick hits the same key.
	cu := newTestCrontab()
	var uf atomic.Int32
	if err := cu.AddJob("30 1 * * *", func() { uf.Add(1) }); err != nil {
		t.Fatal(err)
	}
	utcInstant := time.Date(2025, 11, 2, 1, 30, 0, 0, time.UTC)
	cu.runDue(utcInstant)
	cu.wg.Wait()
	cu.runDue(utcInstant)
	cu.wg.Wait()
	if got := uf.Load(); got != 1 {
		t.Fatalf("UTC single instant should fire once, got %d", got)
	}
}

func TestOverlapSkip(t *testing.T) {
	c := newTestCrontab()
	release := make(chan struct{})
	var fires atomic.Int32
	if err := c.AddJob("* * * * *", func() {
		fires.Add(1)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	c.runDue(base)                  // fires; job blocks, running flag stays set
	c.runDue(base.Add(time.Minute)) // still in flight, CAS fails, tick skipped
	waitCount(t, &fires, 1)         // the first fire landed
	close(release)
	c.wg.Wait()
	if got := fires.Load(); got != 1 {
		t.Fatalf("non-overlap should skip the tick while in flight, got %d", got)
	}
}

func TestOverlapAllowed(t *testing.T) {
	c := newTestCrontab()
	c.overlap = true
	release := make(chan struct{})
	var fires atomic.Int32
	if err := c.AddJob("* * * * *", func() {
		fires.Add(1)
		<-release
	}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	c.runDue(base)
	c.runDue(base.Add(time.Minute)) // overlap allowed: fires again despite first in flight
	waitCount(t, &fires, 2)
	close(release)
	c.wg.Wait()
	if got := fires.Load(); got != 2 {
		t.Fatalf("WithOverlap should fire twice, got %d", got)
	}
}

// TestSlowJobDoesNotStall proves a blocking job does not hold up an unrelated job on the
// same table: fire is async, so runDue never waits on a job to finish.
func TestSlowJobDoesNotStall(t *testing.T) {
	c := newTestCrontab()
	slowRelease := make(chan struct{})
	var slow, fast atomic.Int32
	if err := c.AddJob("* * * * *", func() {
		slow.Add(1)
		<-slowRelease
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.AddJob("* * * * *", func() { fast.Add(1) }); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	c.runDue(base)
	waitCount(t, &fast, 1) // fast completes while slow is still blocked: fire is async, runDue never waits
	waitCount(t, &slow, 1) // slow was dispatched too, just parked in its blocking body
	close(slowRelease)
	c.wg.Wait()
}

func TestPanicRecovery(t *testing.T) {
	c := newTestCrontab()
	var gotSpec atomic.Pointer[string]
	c.onPanic = func(spec string, _ any) { s := spec; gotSpec.Store(&s) }
	var good atomic.Int32
	if err := c.AddJob("* * * * *", func() { panic("boom") }); err != nil {
		t.Fatal(err)
	}
	if err := c.AddJob("* * * * *", func() { good.Add(1) }); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	c.runDue(base)
	c.wg.Wait()
	if p := gotSpec.Load(); p == nil || *p != "* * * * *" {
		t.Fatalf("panic handler should receive the job spec, got %v", p)
	}
	if got := good.Load(); got != 1 {
		t.Fatalf("a sibling job should still fire when another panics, got %d", got)
	}

	c.runDue(base.Add(time.Minute)) // scheduler survives the panic
	c.wg.Wait()
	if got := good.Load(); got != 2 {
		t.Fatalf("scheduler should keep firing after a panic, got %d", got)
	}
}

func TestShutdownDrains(t *testing.T) {
	c := newTestCrontab()
	release := make(chan struct{})
	var done atomic.Bool
	if err := c.AddJob("* * * * *", func() {
		<-release
		done.Store(true)
	}); err != nil {
		t.Fatal(err)
	}
	c.runDue(time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)) // job in flight, blocked

	go close(release) // let it finish while Shutdown is draining
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown with no deadline should drain cleanly, got %v", err)
	}
	if !done.Load() {
		t.Fatal("Shutdown should have waited for the in-flight job to finish")
	}
}

func TestShutdownTimeout(t *testing.T) {
	c := newTestCrontab()
	block := make(chan struct{})
	if err := c.AddJob("* * * * *", func() { <-block }); err != nil {
		t.Fatal(err)
	}
	c.runDue(time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)) // job in flight, will not finish

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown past the deadline should return DeadlineExceeded, got %v", err)
	}

	close(block) // release the orphan so it does not outlive the test
	c.wg.Wait()
}

func TestShutdownIdempotent(t *testing.T) {
	c := newTestCrontab()
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown should be clean, got %v", err)
	}
	if err := c.AddJob("* * * * *", func() {}); !errors.Is(err, ErrClosed) {
		t.Fatalf("AddJob after Shutdown should return ErrClosed, got %v", err)
	}
}

// TestShutdownDuringDispatch stresses the window where the dispatch loop (wg.Add) runs
// concurrently with Shutdown's wg.Wait: sync.WaitGroup panics "Add called concurrently with
// Wait" if a fast job walks the counter through zero while the next job is being dispatched.
// runDue is called from a single goroutine here (Shutdown never calls it), matching production.
// Run under -race with a high count to shake the window loose.
func TestShutdownDuringDispatch(t *testing.T) {
	for range 200 {
		c := newTestCrontab()
		for range 16 {
			if err := c.AddJob("* * * * *", func() {}); err != nil {
				t.Fatal(err)
			}
		}
		base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
		go c.runDue(base)
		if err := c.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown should drain cleanly, got %v", err)
		}
	}
}

// TestNoGoroutineLeak starts a real scheduler (with its run goroutine), shuts it down, and
// confirms the goroutine count settles back: the run loop exits when stop closes because the
// ticker is a local reaped on loop return, not a struct field that could outlive it.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	c := New()
	if err := c.AddJob("* * * * *", func() {}); err != nil {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("run goroutine leaked: started at %d, still %d after Shutdown", before, runtime.NumGoroutine())
}
