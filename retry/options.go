package retry

import "time"

// DefaultMaxRetries is the default number of retries: 3 total attempts (1 initial + 2 retries).
// When used with Do, the function will be called at most 3 times.
const DefaultMaxRetries = 3

// DefaultDelayStep is the default delay step applied in the exponential backoff calculation.
// Between attempt i (0-indexed) and the next, the delay is delayStep*(i+1) before jitter is applied.
// With DefaultDelayStep=1s, the delays between attempts are approximately 1s, 2s, 3s (before jitter).
const DefaultDelayStep = 1 * time.Second

// DefaultJitter is the default setting for adding randomness to delays.
// When true, jitter adds a random duration between 0 and the current base delay,
// so the actual delay for attempt i ranges from delayStep*(i+1) to 2*delayStep*(i+1).
// When false, delays are deterministic: delayStep*1, delayStep*2, etc.
const DefaultJitter = true

// Option is a function that sets a configuration option for a Retry instance.
// It implements the functional options pattern: each Option is a closure that mutates a *Retry.
// Options are applied in order during New.
type Option func(*Retry)

// WithMaxRetries sets the maximum number of attempts (1 initial + retries).
// For example, WithMaxRetries(5) means the function will be called at most 5 times total.
// Values of 0 or less disable all attempts: the Do loop will not execute and returns nil without calling fn.
func WithMaxRetries(maxRetries int) Option {
	return func(r *Retry) {
		r.maxRetries = maxRetries
	}
}

// WithDelayStep sets the base delay step for the exponential backoff.
// Between attempt i (0-indexed) and the next, the delay is delayStep*(i+1) before jitter.
// A value of 0 disables the inter-attempt delay (no sleep between retries).
// Negative values are treated as 0 by time.Sleep.
func WithDelayStep(delayStep time.Duration) Option {
	return func(r *Retry) {
		r.delayStep = delayStep
	}
}

// WithJitter sets whether random jitter is added to each inter-attempt delay.
// When true, a random duration between 0 and the base delay is added to each sleep,
// reducing the likelihood of thundering herd problems in distributed systems.
// When false, delays are deterministic: delayStep*(i+1) for attempt i (0-indexed).
func WithJitter(jitter bool) Option {
	return func(r *Retry) {
		r.jitter = jitter
	}
}
