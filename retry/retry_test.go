package retry

import (
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

// Test Retry with custom delay step
func TestRetry_WithDelayStep(t *testing.T) {
	delayStep := 100 * time.Millisecond
	startTime := time.Now()

	r := New(WithDelayStep(delayStep))

	_ = r.Do(func() error {
		return errors.New("error")
	})

	elapsedTime := time.Since(startTime)
	expectedMinDuration := delayStep * time.Duration(DefaultMaxRetries-1)

	if elapsedTime < expectedMinDuration {
		t.Errorf("expected at least %v, got %v", expectedMinDuration, elapsedTime)
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
