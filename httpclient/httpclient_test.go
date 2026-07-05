package httpclient

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/etkecc/go-kit/retry"
)

// Each option mutates exactly its config field.
func TestOptions_MutateConfig(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		ok   func(*config) bool
	}{
		{"MaxIdleConns", WithMaxIdleConns(7), func(c *config) bool { return c.maxIdleConns == 7 }},
		{"MaxIdleConnsPerHost", WithMaxIdleConnsPerHost(8), func(c *config) bool { return c.maxIdleConnsPerHost == 8 }},
		{"MaxConnsPerHost", WithMaxConnsPerHost(9), func(c *config) bool { return c.maxConnsPerHost == 9 }},
		{"IdleConnTimeout", WithIdleConnTimeout(time.Minute), func(c *config) bool { return c.idleConnTimeout == time.Minute }},
		{"TLSHandshakeTimeout", WithTLSHandshakeTimeout(3 * time.Second), func(c *config) bool { return c.tlsHandshakeTimeout == 3*time.Second }},
		{"ResponseHeaderTimeout", WithResponseHeaderTimeout(4 * time.Second), func(c *config) bool { return c.responseHeaderTimeout == 4*time.Second }},
		{"ExpectContinueTimeout", WithExpectContinueTimeout(2 * time.Second), func(c *config) bool { return c.expectContinueTimeout == 2*time.Second }},
		{"TLSMinVersion", WithTLSMinVersion(tls.VersionTLS13), func(c *config) bool { return c.tlsMinVersion == tls.VersionTLS13 }},
		{"PerAttemptTimeout", WithPerAttemptTimeout(5 * time.Second), func(c *config) bool { return c.perAttemptTimeout == 5*time.Second }},
		{"MaxRetryAfter", WithMaxRetryAfter(time.Minute), func(c *config) bool { return c.maxRetryAfter == time.Minute }},
		{"RetryNonIdempotent", WithRetryNonIdempotent(true), func(c *config) bool { return c.retryNonIdempotent }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := defaultConfig()
			tt.opt.apply(c)
			if !tt.ok(c) {
				t.Errorf("%s did not mutate its config field", tt.name)
			}
		})
	}
}

// Wrap composes the retry layer onto a BYO client and keeps its Timeout.
func TestWrap_ComposesRetryOntoBYO(t *testing.T) {
	client := Wrap(&http.Client{Timeout: 3 * time.Second}, WithPerAttemptTimeout(time.Second))
	if client.Timeout != 3*time.Second {
		t.Errorf("BYO client Timeout not preserved: %v", client.Timeout)
	}
	if _, ok := client.Transport.(*retryTransport); !ok {
		t.Error("transport was not wrapped in retryTransport")
	}
}

// A BYO client with no transport bases the retry layer on http.DefaultTransport.
func TestWrap_NilTransportUsesDefault(t *testing.T) {
	rt, ok := Wrap(&http.Client{}).Transport.(*retryTransport)
	if !ok {
		t.Fatal("transport is not *retryTransport")
	}
	if rt.base != http.DefaultTransport {
		t.Errorf("base = %T, want http.DefaultTransport", rt.base)
	}
}

// Wrap(nil) is usable: no panic, retry layer over the default transport.
func TestWrap_NilClient(t *testing.T) {
	rt, ok := Wrap(nil).Transport.(*retryTransport)
	if !ok {
		t.Fatal("Wrap(nil) transport is not *retryTransport")
	}
	if rt.base != http.DefaultTransport {
		t.Errorf("base = %T, want http.DefaultTransport", rt.base)
	}
}

// The only call site forcing every retry setter to actually return RetryOption: one typed
// as Option would fail to compile in this variadic.
func TestWrap_AllRetryOptionsApply(t *testing.T) {
	retrier := retry.New(retry.WithMaxRetries(5))
	client := Wrap(&http.Client{},
		WithPerAttemptTimeout(2*time.Second),
		WithMaxRetryAfter(time.Minute),
		WithRetry(retrier),
		WithRetryIf(func(error) bool { return true }),
		WithRetryNonIdempotent(true),
		WithRetryBudget(denyBudget{}),
		WithOnAttempt(func(AttemptInfo) {}),
	)
	rt, ok := client.Transport.(*retryTransport)
	if !ok {
		t.Fatal("transport is not *retryTransport")
	}
	if rt.perAttempt != 2*time.Second {
		t.Errorf("perAttempt = %v, want 2s", rt.perAttempt)
	}
	if rt.maxRetryAfter != time.Minute {
		t.Errorf("maxRetryAfter = %v, want 1m", rt.maxRetryAfter)
	}
	if rt.retrier != retrier {
		t.Error("WithRetry retrier not applied")
	}
	if !rt.nonIdem {
		t.Error("WithRetryNonIdempotent not applied")
	}
	if _, ok := rt.budget.(denyBudget); !ok {
		t.Errorf("budget = %T, want denyBudget", rt.budget)
	}
	if rt.onAttempt == nil {
		t.Error("WithOnAttempt hook not applied")
	}
}

// NewSingleHost sizes all three pool dimensions to DefaultPoolSize, global >= per-host.
func TestNewSingleHost_PoolCoherence(t *testing.T) {
	client := NewSingleHost()
	rt, ok := client.Transport.(*retryTransport)
	if !ok {
		t.Fatal("transport is not *retryTransport")
	}
	tr, ok := rt.base.(*http.Transport)
	if !ok {
		t.Fatal("base is not *http.Transport")
	}
	if tr.MaxIdleConns != DefaultPoolSize || tr.MaxIdleConnsPerHost != DefaultPoolSize || tr.MaxConnsPerHost != DefaultPoolSize {
		t.Errorf("pool = (%d, %d, %d), want all %d",
			tr.MaxIdleConns, tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost, DefaultPoolSize)
	}
	if tr.MaxIdleConnsPerHost > tr.MaxIdleConns {
		t.Error("per-host idle pool exceeds the global idle pool")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("TLS min version default is not TLS 1.2")
	}
}
