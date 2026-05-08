package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Test default Retry behavior without options
func TestRetry_Default(t *testing.T) {
	callCount := int32(0)
	r := New()

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if err == nil {
		t.Errorf("expected error, got nil")
	}

	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls, got %d", DefaultMaxRetries, callCount)
	}
}

// Test Retry with custom number of retries
func TestRetry_WithMaxRetries(t *testing.T) {
	maxRetries := 5
	callCount := int32(0)

	r := New(WithMaxRetries(maxRetries))

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if err == nil {
		t.Errorf("expected error, got nil")
	}

	if callCount != int32(maxRetries) {
		t.Errorf("expected %d calls, got %d", maxRetries, callCount)
	}
}

// Test Retry with custom delay step (jitter disabled for deterministic lower bound)
func TestRetry_WithDelayStep(t *testing.T) {
	delayStep := 100 * time.Millisecond
	startTime := time.Now()

	r := New(WithDelayStep(delayStep), WithJitter(false))

	_ = r.Do(func() error {
		return errors.New("error")
	})

	elapsedTime := time.Since(startTime)
	expectedMinDuration := delayStep * time.Duration(DefaultMaxRetries-1)

	if elapsedTime < expectedMinDuration {
		t.Errorf("expected at least %v, got %v", expectedMinDuration, elapsedTime)
	}
}

// Test that no sleep occurs after the final attempt
func TestRetry_NoSleepAfterFinalAttempt(t *testing.T) {
	delayStep := 50 * time.Millisecond
	// With fix: sleeps delayStep*(1+2)=150ms; without fix: delayStep*(1+2+3)=300ms.
	// 250ms (5*delayStep) sits between the two values and catches the regression.
	maxAllowed := 5 * delayStep

	r := New(WithDelayStep(delayStep), WithJitter(false))
	start := time.Now()
	_ = r.Do(func() error {
		return errors.New("error")
	})
	elapsed := time.Since(start)

	if elapsed >= maxAllowed {
		t.Errorf("expected elapsed < %v (no sleep after final attempt), got %v", maxAllowed, elapsed)
	}
}

// Test Retry with jitter enabled
func TestRetry_WithJitter(t *testing.T) {
	delayStep := 50 * time.Millisecond
	callCount := int32(0)

	r := New(
		WithJitter(true),
		WithDelayStep(delayStep),
	)

	// Use Do to ensure we kick in the retry logic
	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls, got %d", DefaultMaxRetries, callCount)
	}
}

// Test Retry with 0 delay and jitter automatically disabled
func TestRetry_WithDelay0(t *testing.T) {
	callCount := int32(0)

	r := New(
		WithJitter(true),
		WithDelayStep(0),
	)

	// Use Do to ensure we kick in the retry logic
	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls, got %d", DefaultMaxRetries, callCount)
	}
}

// Test Retry success on first try
func TestRetry_SuccessOnFirstTry(t *testing.T) {
	r := New()

	err := r.Do(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("expected nil, got error: %v", err)
	}
}

// Test Retry success on nth try
func TestRetry_SuccessOnNthTry(t *testing.T) {
	n := 2
	attempt := 0

	r := New()

	err := r.Do(func() error {
		attempt++
		if attempt == n {
			return nil
		}
		return errors.New("error")
	})
	if err != nil {
		t.Errorf("expected nil, got error: %v", err)
	}

	if attempt != n {
		t.Errorf("expected attempt %d, got %d", n, attempt)
	}
}

// Test DoCtx returns ctx.Err() immediately when context is already canceled, fn not invoked
func TestRetry_DoCtx_AlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	r := New()
	err := r.DoCtx(ctx, func() error {
		called = true
		return nil
	})

	if called {
		t.Error("expected fn not to be called with pre-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// Test DoCtx returns within milliseconds when context is canceled during sleep
func TestRetry_DoCtx_CancelDuringSleep(t *testing.T) {
	delayStep := 100 * time.Millisecond
	r := New(WithDelayStep(delayStep), WithJitter(false))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.DoCtx(ctx, func() error {
		return errors.New("error")
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed >= 50*time.Millisecond {
		t.Errorf("expected fast return on ctx cancellation, got %v", elapsed)
	}
}

// Test DoCtx returns context.DeadlineExceeded when deadline expires mid-loop
func TestRetry_DoCtx_DeadlineExceeded(t *testing.T) {
	delayStep := 100 * time.Millisecond
	r := New(WithDelayStep(delayStep), WithJitter(false))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := r.DoCtx(ctx, func() error {
		return errors.New("error")
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

// Test WithRetryIf predicate=always-true behaves identically to default (max attempts)
func TestRetry_WithRetryIf_AlwaysTrue(t *testing.T) {
	callCount := int32(0)
	r := New(WithRetryIf(func(_ error) bool { return true }), WithDelayStep(0))

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls, got %d", DefaultMaxRetries, callCount)
	}
}

// Test WithRetryIf predicate=always-false aborts after first attempt
func TestRetry_WithRetryIf_AlwaysFalse(t *testing.T) {
	callCount := int32(0)
	r := New(WithRetryIf(func(_ error) bool { return false }), WithDelayStep(0))

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (immediate abort), got %d", callCount)
	}
}

// Test WithRetryIf(nil) preserves default behavior
func TestRetry_WithRetryIf_Nil(t *testing.T) {
	callCount := int32(0)
	r := New(WithRetryIf(nil), WithDelayStep(0))

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls (default behavior preserved), got %d", DefaultMaxRetries, callCount)
	}
}

var errSentinel = errors.New("sentinel")

// Test WithRetryIf predicate using errors.Is routes correctly
func TestRetry_WithRetryIf_ErrorsIs(t *testing.T) {
	callCount := int32(0)
	r := New(
		WithRetryIf(func(err error) bool { return !errors.Is(err, errSentinel) }),
		WithDelayStep(0),
	)

	err := r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errSentinel
	})

	if !errors.Is(err, errSentinel) {
		t.Errorf("expected errSentinel, got %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (abort on sentinel), got %d", callCount)
	}
}

// Test jitter distribution: 1000 samples in [0, base), mean within [40ms, 60ms]
func TestRetry_Jitter_Distribution(t *testing.T) {
	base := 100 * time.Millisecond
	r := New()

	const samples = 1000
	var total time.Duration
	for range samples {
		d := r.calculateJitter(base)
		if d < 0 || d >= base {
			t.Errorf("jitter result %v out of range [0, %v)", d, base)
		}
		total += d
	}

	mean := total / time.Duration(samples)
	if mean < 40*time.Millisecond || mean > 60*time.Millisecond {
		t.Errorf("jitter mean %v outside expected [40ms, 60ms]", mean)
	}
}

// Test jitter never returns a value >= base (catches regression to additive form)
func TestRetry_Jitter_NeverExceedsBase(t *testing.T) {
	base := 100 * time.Millisecond
	r := New()

	for range 1000 {
		d := r.calculateJitter(base)
		if d >= base {
			t.Errorf("jitter result %v >= base %v (additive regression?)", d, base)
		}
	}
}

// Test zero-value Retry (bypassing New) does not panic and calls fn at least once
func TestRetry_ZeroValue_NoPanic(t *testing.T) {
	called := false
	r := &Retry{} // maxRetries=0, retryIf=nil — both guarded inside DoCtx
	err := r.Do(func() error {
		called = true
		return errors.New("error")
	})
	if !called {
		t.Error("expected fn to be called at least once")
	}
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// Test New with nil option does not panic
func TestNew_NilOption(t *testing.T) {
	r := New(nil)
	if r == nil {
		t.Fatal("expected non-nil Retry")
	}
	// should behave identically to New()
	callCount := int32(0)
	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})
	if callCount != int32(DefaultMaxRetries) {
		t.Errorf("expected %d calls, got %d", DefaultMaxRetries, callCount)
	}
}

// Test nil receiver returns ErrNilRetry, does not panic
func TestRetry_NilReceiver(t *testing.T) {
	var r *Retry
	err := r.Do(func() error { return nil })
	if !errors.Is(err, ErrNilRetry) {
		t.Errorf("expected ErrNilRetry, got %v", err)
	}
}

// Test DoCtx with nil context returns ErrNilContext, does not panic
func TestRetry_DoCtx_NilContext(t *testing.T) {
	r := New()
	err := r.DoCtx(nil, func() error { return nil }) //nolint:staticcheck // intentionally passing nil ctx to verify the nil guard
	if !errors.Is(err, ErrNilContext) {
		t.Errorf("expected ErrNilContext, got %v", err)
	}
}

// Test Do/DoCtx with nil fn returns ErrNilFn, does not panic
func TestRetry_NilFn(t *testing.T) {
	r := New()
	err := r.Do(nil)
	if !errors.Is(err, ErrNilFn) {
		t.Errorf("expected ErrNilFn, got %v", err)
	}
}

// Test WithMaxRetries(0) is clamped to 1: fn invoked exactly once
func TestRetry_Clamp_Zero(t *testing.T) {
	callCount := int32(0)
	r := New(WithMaxRetries(0), WithDelayStep(0))

	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if callCount != 1 {
		t.Errorf("expected 1 invocation (clamped from 0), got %d", callCount)
	}
}

// Test WithMaxRetries(-5) is clamped to 1: fn invoked exactly once
func TestRetry_Clamp_Negative(t *testing.T) {
	callCount := int32(0)
	r := New(WithMaxRetries(-5), WithDelayStep(0))

	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if callCount != 1 {
		t.Errorf("expected 1 invocation (clamped from -5), got %d", callCount)
	}
}

// Test WithMaxRetries(1) invokes fn exactly once (regression guard)
func TestRetry_Clamp_One(t *testing.T) {
	callCount := int32(0)
	r := New(WithMaxRetries(1), WithDelayStep(0))

	_ = r.Do(func() error {
		atomic.AddInt32(&callCount, 1)
		return errors.New("error")
	})

	if callCount != 1 {
		t.Errorf("expected 1 invocation, got %d", callCount)
	}
}
