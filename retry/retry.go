// Package retry provides a simple retry mechanism for Go with exponential backoff and jitter.
//
// # Usage (simple)
//
// Create a retrier with default settings and execute a function:
//
//	err := retry.New().Do(func() error {
//		return errors.New("this will be retried")
//	})
//	if err != nil {
//		fmt.Println("error:", err)
//	}
//
// # Usage (with options)
//
// Create a retrier with custom settings:
//
//	r := retry.New(
//		retry.WithMaxRetries(5),
//		retry.WithDelayStep(100*time.Millisecond),
//		retry.WithJitter(true),
//	)
//	err := r.Do(func() error {
//		return errors.New("this will be retried")
//	})
//	if err != nil {
//		fmt.Println("error:", err)
//	}
package retry

import (
	"math/rand"
	"time"
)

// Retry holds the configuration for executing a function with retry logic.
// It implements exponential backoff with optional jitter to handle transient failures.
//
// The Retry struct is not safe for concurrent use across goroutines if options are mutated
// during Do execution. Do itself is stateless with respect to the struct fields.
// Always construct a Retry using New rather than creating a zero value directly;
// the zero value has maxRetries=0, delayStep=0, jitter=false, which means Do will not retry.
type Retry struct {
	maxRetries int
	delayStep  time.Duration
	jitter     bool
}

// New creates a new Retry instance with default settings, then applies the given options.
//
// Default settings applied by New:
//   - maxRetries: DefaultMaxRetries (3 total attempts)
//   - delayStep: DefaultDelayStep (1 second)
//   - jitter: DefaultJitter (true)
//
// Options are applied in the order they are passed. Each option overrides
// the corresponding default value.
func New(options ...Option) *Retry {
	r := &Retry{
		maxRetries: DefaultMaxRetries,
		delayStep:  DefaultDelayStep,
		jitter:     DefaultJitter,
	}

	for _, opt := range options {
		opt(r)
	}

	return r
}

// Do executes fn repeatedly until it returns nil or the maximum number of attempts is exhausted.
//
// The function implements exponential backoff with optional jitter:
//   - Executes fn immediately (attempt 1)
//   - If fn returns nil, Do returns nil immediately
//   - If fn returns an error, Do waits and retries (up to maxRetries total attempts)
//   - Between attempt i (0-indexed) and the next, Do sleeps for delayStep*(i+1)
//   - If jitter is enabled, a random duration between 0 and the base delay is added
//   - After the final attempt, no sleep occurs
//   - If all attempts fail, Do returns the error from the last attempt
//
// The delay sequence for the default configuration (delayStep=1s) without jitter is:
// attempt 1 (immediate), sleep 1s, attempt 2, sleep 2s, attempt 3
//
// With jitter enabled, each sleep duration is in the range [base, 2*base].
func (r *Retry) Do(fn func() error) error {
	var err error
	for i := range r.maxRetries {
		if err = fn(); err == nil {
			return nil
		}

		delay := r.delayStep * time.Duration(i+1)
		if r.jitter {
			delay = r.calculateJitter(delay)
		}

		time.Sleep(delay)
	}

	return err
}

// calculateJitter calculates a random jitter value to be added to the delay,
// based on the delay value
func (r *Retry) calculateJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}

	jitter := rand.Int63n(delay.Nanoseconds()) //nolint:gosec // that's fine for this use case
	return delay + time.Duration(jitter)
}
