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
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

var (
	// ErrNilRetry is returned by Do/DoCtx when called on a nil *Retry receiver.
	ErrNilRetry = errors.New("retry: nil retrier")
	// ErrNilContext is returned by DoCtx when a nil context is passed.
	ErrNilContext = errors.New("retry: nil context")
	// ErrNilFn is returned by Do/DoCtx when a nil function is passed.
	ErrNilFn = errors.New("retry: nil fn")
)

// Retry holds the configuration for executing a function with retry logic.
// It implements exponential backoff with optional jitter to handle transient failures.
//
// The Retry struct is not safe for concurrent use across goroutines if options are mutated
// during Do execution. Do itself is stateless with respect to the struct fields.
type Retry struct {
	maxRetries int
	delayStep  time.Duration
	retryIf    func(error) bool
	jitter     bool
}

// New creates a new Retry instance with default settings, then applies the given options.
//
// Default settings applied by New:
//   - maxRetries: DefaultMaxRetries (3 total attempts)
//   - delayStep: DefaultDelayStep (1 second)
//   - jitter: DefaultJitter (true)
//   - retryIf: retry on any non-nil error
//
// Options are applied in the order they are passed. Nil options are ignored.
// Values of maxRetries less than 1 are clamped to 1, ensuring fn is invoked at least once.
func New(options ...Option) *Retry {
	r := &Retry{
		maxRetries: DefaultMaxRetries,
		delayStep:  DefaultDelayStep,
		jitter:     DefaultJitter,
		retryIf:    func(err error) bool { return err != nil },
	}

	for _, opt := range options {
		if opt != nil {
			opt(r)
		}
	}

	if r.maxRetries < 1 {
		r.maxRetries = 1
	}

	return r
}

// DoCtx executes fn repeatedly until it returns nil, the context is canceled,
// the retryIf predicate returns false, or the maximum number of attempts is exhausted.
//
// Returns ErrNilRetry, ErrNilContext, or ErrNilFn immediately if the receiver,
// ctx, or fn are nil respectively.
//
// The function implements exponential backoff with optional jitter:
//   - Checks ctx.Err() before each attempt; returns ctx.Err() immediately if set
//   - Executes fn immediately (attempt 1)
//   - If fn returns nil, DoCtx returns nil immediately
//   - If retryIf returns false for the error, DoCtx returns the error immediately
//   - Between attempts, DoCtx sleeps for delayStep*(i+1); ctx cancellation interrupts the sleep
//   - If jitter is enabled, the delay is a random duration in [0, base) — AWS full jitter
//   - After the final attempt, no sleep occurs
//   - If all attempts fail, DoCtx returns the error from the last attempt
func (r *Retry) DoCtx(ctx context.Context, fn func() error) error {
	if err := r.validate(ctx, fn); err != nil {
		return err
	}

	maxRetries := max(r.maxRetries, 1)

	var err error
	for i := range maxRetries {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err = fn(); err == nil {
			return nil
		}
		if !r.shouldRetry(err) {
			return err
		}
		if i == maxRetries-1 {
			break
		}
		delay := r.delayStep * time.Duration(i+1)
		if r.jitter {
			delay = r.calculateJitter(delay)
		}
		if err := r.sleep(ctx, delay); err != nil {
			return err
		}
	}
	return err
}

// validate checks for nil receiver, ctx, and fn before the retry loop.
func (r *Retry) validate(ctx context.Context, fn func() error) error {
	if r == nil {
		return ErrNilRetry
	}
	if ctx == nil {
		return ErrNilContext
	}
	if fn == nil {
		return ErrNilFn
	}
	return nil
}

// shouldRetry reports whether err warrants another attempt.
func (r *Retry) shouldRetry(err error) bool {
	if r.retryIf == nil {
		return true
	}
	return r.retryIf(err)
}

// Do executes fn repeatedly until it returns nil or the maximum number of attempts is exhausted.
// It is equivalent to DoCtx(context.Background(), fn).
//
// See DoCtx for full documentation.
func (r *Retry) Do(fn func() error) error {
	return r.DoCtx(context.Background(), fn)
}

// sleep waits for delay or until ctx is done, whichever comes first.
func (r *Retry) sleep(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// calculateJitter returns a random delay in [0, base) — AWS full jitter.
func (r *Retry) calculateJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(delay.Nanoseconds())) //nolint:gosec // jitter does not require cryptographic randomness
}
