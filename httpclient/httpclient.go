// Package httpclient builds an *http.Client tuned for talking to one backend a lot:
// a right-sized connection pool, a deadline on each attempt, and transparent retries
// that only replay requests it is safe to replay.
//
// New is the general constructor; NewSingleHost presets the pool for a single backend.
// Both return a plain *http.Client used like any other. See the examples for usage.
//
// Zero external dependencies: stdlib plus the go-kit retry sibling.
package httpclient

import (
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/etkecc/go-kit/retry"
)

var (
	// ErrNonReplayableBody guards against silent body corruption on retry: it is returned by
	// RoundTrip when a request is eligible for retry (idempotent method, or opted in via
	// WithRetryNonIdempotent) but its body has no GetBody to rewind. Retrying would replay an
	// already-consumed reader, so it fails loud instead of sending a truncated body on the
	// second attempt.
	ErrNonReplayableBody = errors.New("httpclient: retryable request has a body but no GetBody to rewind it")

	// ErrTransportConflict is returned when WithHTTPClient supplies a base client and a
	// transport-tuning option is also set. A BYO client owns transport tuning; honoring
	// both would silently drop one, so construction stops here.
	ErrTransportConflict = errors.New("httpclient: WithHTTPClient conflicts with transport-tuning options")

	errAttemptTimeout  = errors.New("httpclient: per-attempt timeout")
	errBudgetExhausted = errors.New("httpclient: retry budget exhausted")
)

// AttemptInfo is passed to the WithOnAttempt hook after every attempt, so a caller can
// observe a retry storm as it happens.
type AttemptInfo struct {
	Method   string
	Host     string
	Attempt  int           // 1-based
	Status   int           // 0 when the attempt errored before any response
	Err      error         // nil on a response, even a retryable one
	Elapsed  time.Duration // wall time for this attempt alone
	Retrying bool          // the attempt was retry-eligible and handed back to the retrier, which may still stop on exhausted attempts or budget
	Reused   bool          // the connection was reused (via httptrace, when a conn was got)
}

// RetryBudget gates retries across requests to bound retry amplification during an
// outage. Allow is consulted before each retry, never before the first attempt. Record
// reports whether a retry was performed (true) or denied by the budget (false), so a
// token-bucket implementation can account for spend; it is not a success/failure signal.
// The default implementation always allows; supply a token bucket to cap. Implementations
// must be safe for concurrent use: a shared client calls this from every RoundTrip.
type RetryBudget interface {
	Allow() bool
	Record(retried bool)
}

// noopBudget is the default RetryBudget: it allows every retry.
type noopBudget struct{}

func (noopBudget) Allow() bool   { return true }
func (noopBudget) Record(_ bool) {}

// config is the mutable target of the functional options. defaultConfig seeds every
// field; options overwrite; build turns it into a wired *http.Client.
type config struct {
	maxIdleConns        int
	maxIdleConnsPerHost int
	maxConnsPerHost     int

	idleConnTimeout       time.Duration
	tlsHandshakeTimeout   time.Duration
	responseHeaderTimeout time.Duration
	expectContinueTimeout time.Duration
	perAttemptTimeout     time.Duration

	protocols     *http.Protocols
	http2         *http.HTTP2Config
	tlsMinVersion uint16

	retrier            *retry.Retry
	retryIf            func(error) bool
	retryNonIdempotent bool
	maxRetryAfter      time.Duration
	budget             RetryBudget

	baseClient *http.Client
	onAttempt  func(AttemptInfo)

	// transportConfigured records that a transport-tuning option was set, so build can
	// reject the WithHTTPClient conflict regardless of option order.
	transportConfigured bool
}

// New builds a general-purpose retrying client, suitable for many hosts: per-host
// connections stay uncapped and the idle pool is sized for throughput.
func New(opts ...Option) (*http.Client, error) {
	return defaultConfig().apply(opts).build()
}

// NewSingleHost presets the pool for one backend: all three pool dimensions sized to
// DefaultPoolSize (per-host connections bounded, not merely idle-capped) plus HTTP/2
// keepalive pings to notice a dead persistent connection.
func NewSingleHost(opts ...Option) (*http.Client, error) {
	return defaultConfig().apply(append([]Option{singleHostPreset()}, opts...)).build()
}

// apply runs the options in order, skipping nils, and returns the config for chaining.
func (c *config) apply(opts []Option) *config {
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// build wires the config into a *http.Client, or returns a loud error.
func (c *config) build() (*http.Client, error) {
	if c.baseClient != nil {
		return c.buildFromBase()
	}
	return &http.Client{Transport: c.newRetryTransport(c.newTransport())}, nil
}

// buildFromBase wraps a caller-supplied client's transport in the retry layer, refusing
// any transport-tuning option since a BYO client owns that.
func (c *config) buildFromBase() (*http.Client, error) {
	if c.transportConfigured {
		return nil, ErrTransportConflict
	}
	base := c.baseClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport:     c.newRetryTransport(base),
		CheckRedirect: c.baseClient.CheckRedirect,
		Jar:           c.baseClient.Jar,
		Timeout:       c.baseClient.Timeout,
	}, nil
}

// newRetryTransport wraps base in the retrying RoundTripper from the current config.
func (c *config) newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{
		base:          base,
		retrier:       c.buildRetrier(),
		perAttempt:    c.perAttemptTimeout,
		nonIdem:       c.retryNonIdempotent,
		maxRetryAfter: c.maxRetryAfter,
		budget:        c.budget,
		onAttempt:     c.onAttempt,
	}
}

// buildRetrier returns the caller's retrier via WithRetry, or a default one with the
// classifier baked in once (static, not rebuilt per request).
func (c *config) buildRetrier() *retry.Retry {
	if c.retrier != nil {
		return c.retrier
	}
	classify := c.retryIf
	if classify == nil {
		classify = defaultRetryIf
	}
	return retry.New(
		retry.WithMaxRetries(defaultMaxRetries),
		retry.WithDelayStep(defaultDelayStep),
		retry.WithJitter(true),
		retry.WithRetryIf(classify),
	)
}

// newTransport builds the base *http.Transport from the pool and protocol config. The
// client Timeout stays 0: retryTransport owns per-attempt deadlines, and a client Timeout
// would cap the whole retry sequence instead, killing a legitimate second try.
func (c *config) newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          c.maxIdleConns,
		MaxIdleConnsPerHost:   c.maxIdleConnsPerHost,
		MaxConnsPerHost:       c.maxConnsPerHost,
		IdleConnTimeout:       c.idleConnTimeout,
		TLSHandshakeTimeout:   c.tlsHandshakeTimeout,
		ResponseHeaderTimeout: c.responseHeaderTimeout,
		ExpectContinueTimeout: c.expectContinueTimeout,
		Protocols:             c.protocols,
		HTTP2:                 c.http2,
		TLSClientConfig:       &tls.Config{MinVersion: c.tlsMinVersion},
	}
}
