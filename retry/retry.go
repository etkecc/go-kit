// Package retry provides a simple retry mechanism for Go with exponential backoff and jitter.
//
// Usage (simple):
//
// ```
//
//	err := retry.New().Do(func() error {
//		return errors.New("this will be retried")
//	})
//
//	if err != nil {
//		fmt.Println("error:", err)
//	}
//
// ```
//
// Usage (with options):
//
// ```
//
// r := retry.New(
//
//	retry.WithMaxRetries(5),
//	retry.WithDelayStep(100*time.Millisecond),
//	retry.WithJitter(true),
//
// )
//
//	err := r.Do(func() error {
//		return errors.New("this will be retried")
//	})
//
//	if err != nil {
//		fmt.Println("error:", err)
//	}
//
// ```
package retry

import (
	"math/rand"
	"time"
)

// Retry is a struct that holds the configuration for the retrier
type Retry struct {
	maxRetries int
	delayStep  time.Duration
	jitter     bool
}

// New creates a new retrier with the given options
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

// Do retries the given function until it returns nil or the maximum number of retries is reached
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
	jitter := rand.Int63n(delay.Nanoseconds()) //nolint:gosec // that's fine for this use case
	return delay + time.Duration(jitter)
}
