package retry

import "time"

// DefaultMaxRetries is the default number of retries
const DefaultMaxRetries = 3

// DefaultDelayStep is the default delay step, multiplied by the retry number
const DefaultDelayStep = 1 * time.Second

// DefaultJitter is the default value for jitter
const DefaultJitter = true

// Option is a function that sets a configuration option for a retrier
type Option func(*Retry)

// WithMaxRetries sets the maximum number of retries
func WithMaxRetries(maxRetries int) Option {
	return func(r *Retry) {
		r.maxRetries = maxRetries
	}
}

// WithDelayStep sets the delay step, multiplied by the retry number
func WithDelayStep(delayStep time.Duration) Option {
	return func(r *Retry) {
		r.delayStep = delayStep
	}
}

// WithJitter sets the jitter value
func WithJitter(jitter bool) Option {
	return func(r *Retry) {
		r.jitter = jitter
	}
}
