package httpclient

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/etkecc/go-kit/retry"
)

// Exported defaults, safe to reference when overriding one knob relative to another.
const (
	// DefaultPoolSize is the connection-pool size NewSingleHost applies to all three pool
	// dimensions, overriding the stdlib default of 2 idle connections per host.
	DefaultPoolSize = 256
	// DefaultIdleConnTimeout is how long an idle connection is kept before closing.
	DefaultIdleConnTimeout = 90 * time.Second
	// DefaultPerAttemptTimeout is the deadline applied to each individual attempt.
	DefaultPerAttemptTimeout = 10 * time.Second
	// DefaultMaxRetryAfter is the ceiling on an honored Retry-After: past it, the response
	// is returned live instead of waiting.
	DefaultMaxRetryAfter = 30 * time.Second
)

const (
	defaultMaxRetries            = 3
	defaultDelayStep             = 200 * time.Millisecond
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 10 * time.Second
	defaultExpectContinueTimeout = 1 * time.Second
	defaultH2PingInterval        = 15 * time.Second
	defaultH2PingTimeout         = 15 * time.Second
)

// Option configures a client during New or NewSingleHost.
type Option func(*config)

// defaultConfig seeds the shared defaults both constructors start from.
func defaultConfig() *config {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &config{
		maxIdleConns:          DefaultPoolSize,
		maxIdleConnsPerHost:   DefaultPoolSize,
		maxConnsPerHost:       0,
		idleConnTimeout:       DefaultIdleConnTimeout,
		tlsHandshakeTimeout:   defaultTLSHandshakeTimeout,
		responseHeaderTimeout: defaultResponseHeaderTimeout,
		expectContinueTimeout: defaultExpectContinueTimeout,
		perAttemptTimeout:     DefaultPerAttemptTimeout,
		protocols:             protocols,
		tlsMinVersion:         tls.VersionTLS12,
		maxRetryAfter:         DefaultMaxRetryAfter,
		budget:                noopBudget{},
	}
}

// singleHostPreset caps per-host connections to the pool size and enables HTTP/2 keepalive
// pings, on top of the shared defaults. Applied before caller options, so callers still win.
func singleHostPreset() Option {
	return func(c *config) {
		WithMaxConnsPerHost(DefaultPoolSize)(c)
		WithHTTP2Config(&http.HTTP2Config{
			SendPingTimeout: defaultH2PingInterval,
			PingTimeout:     defaultH2PingTimeout,
		})(c)
	}
}

// WithMaxIdleConns sets the total idle-connection pool size across all hosts.
func WithMaxIdleConns(n int) Option {
	return func(c *config) { c.maxIdleConns = n; c.transportConfigured = true }
}

// WithMaxIdleConnsPerHost sets the idle-connection pool size per host.
func WithMaxIdleConnsPerHost(n int) Option {
	return func(c *config) { c.maxIdleConnsPerHost = n; c.transportConfigured = true }
}

// WithMaxConnsPerHost caps total (active plus idle) connections per host; 0 is unlimited.
func WithMaxConnsPerHost(n int) Option {
	return func(c *config) { c.maxConnsPerHost = n; c.transportConfigured = true }
}

// WithIdleConnTimeout sets how long an idle connection is kept before closing.
func WithIdleConnTimeout(d time.Duration) Option {
	return func(c *config) { c.idleConnTimeout = d; c.transportConfigured = true }
}

// WithTLSHandshakeTimeout sets the TLS handshake deadline.
func WithTLSHandshakeTimeout(d time.Duration) Option {
	return func(c *config) { c.tlsHandshakeTimeout = d; c.transportConfigured = true }
}

// WithResponseHeaderTimeout sets how long to wait for response headers after the request.
func WithResponseHeaderTimeout(d time.Duration) Option {
	return func(c *config) { c.responseHeaderTimeout = d; c.transportConfigured = true }
}

// WithExpectContinueTimeout sets the wait for a 100-Continue after Expect headers.
func WithExpectContinueTimeout(d time.Duration) Option {
	return func(c *config) { c.expectContinueTimeout = d; c.transportConfigured = true }
}

// WithProtocols sets the HTTP protocols the transport negotiates.
func WithProtocols(p *http.Protocols) Option {
	return func(c *config) { c.protocols = p; c.transportConfigured = true }
}

// WithHTTP2Config sets the transport's HTTP/2 configuration.
func WithHTTP2Config(h2 *http.HTTP2Config) Option {
	return func(c *config) { c.http2 = h2; c.transportConfigured = true }
}

// WithTLSMinVersion sets the minimum accepted TLS version (e.g. tls.VersionTLS13).
func WithTLSMinVersion(v uint16) Option {
	return func(c *config) { c.tlsMinVersion = v; c.transportConfigured = true }
}

// WithPerAttemptTimeout sets the deadline applied to each attempt; 0 disables it.
func WithPerAttemptTimeout(d time.Duration) Option {
	return func(c *config) { c.perAttemptTimeout = d }
}

// WithMaxRetryAfter sets the ceiling on an honored Retry-After header.
func WithMaxRetryAfter(d time.Duration) Option {
	return func(c *config) { c.maxRetryAfter = d }
}

// WithRetry replaces the default retrier. The caller then owns backoff, jitter, and the
// retry predicate; WithRetryIf is ignored once this is set.
func WithRetry(r *retry.Retry) Option {
	return func(c *config) { c.retrier = r }
}

// WithRetryIf overrides the predicate deciding which errors are retryable. Ignored when
// WithRetry supplies a full retrier.
func WithRetryIf(predicate func(error) bool) Option {
	return func(c *config) { c.retryIf = predicate }
}

// WithRetryNonIdempotent opts non-idempotent methods (POST, PATCH) into retry. Off by
// default: a replayed POST can double-apply a side effect.
func WithRetryNonIdempotent(on bool) Option {
	return func(c *config) { c.retryNonIdempotent = on }
}

// WithRetryBudget sets the cross-request retry budget.
func WithRetryBudget(b RetryBudget) Option {
	return func(c *config) {
		if b != nil {
			c.budget = b
		}
	}
}

// WithHTTPClient wraps a caller-supplied client instead of building a transport. Combining
// it with any transport-tuning option makes construction return ErrTransportConflict.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) { c.baseClient = client }
}

// WithOnAttempt registers a hook called after every attempt with its AttemptInfo. The hook
// runs on every concurrent RoundTrip of a shared client, so it must be safe for concurrent use.
func WithOnAttempt(hook func(AttemptInfo)) Option {
	return func(c *config) { c.onAttempt = hook }
}
