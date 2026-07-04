package httpclient

import (
	"crypto/tls"
	"errors"
	"net/http"
	"testing"
	"time"
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
			tt.opt(c)
			if !tt.ok(c) {
				t.Errorf("%s did not mutate its config field", tt.name)
			}
		})
	}
}

// A transport-tuning option sets transportConfigured so it conflicts with a BYO client.
func TestOptions_TransportKnobsFlagged(t *testing.T) {
	knobs := []struct {
		name string
		opt  Option
	}{
		{"MaxIdleConns", WithMaxIdleConns(1)},
		{"IdleConnTimeout", WithIdleConnTimeout(time.Second)},
		{"Protocols", WithProtocols(new(http.Protocols))},
		{"HTTP2Config", WithHTTP2Config(new(http.HTTP2Config))},
		{"TLSMinVersion", WithTLSMinVersion(tls.VersionTLS13)},
	}
	for _, tt := range knobs {
		t.Run(tt.name, func(t *testing.T) {
			c := defaultConfig()
			tt.opt(c)
			if !c.transportConfigured {
				t.Errorf("%s should have flagged transportConfigured", tt.name)
			}
		})
	}
}

// A retry-layer option does NOT flag transportConfigured, so it composes with a BYO client.
func TestOptions_RetryLayerNotFlagged(t *testing.T) {
	for _, opt := range []Option{
		WithPerAttemptTimeout(time.Second),
		WithMaxRetryAfter(time.Second),
		WithRetryNonIdempotent(true),
		WithOnAttempt(func(AttemptInfo) {}),
	} {
		c := defaultConfig()
		opt(c)
		if c.transportConfigured {
			t.Error("retry-layer option must not flag transportConfigured")
		}
	}
}

func TestNew_BYOTransportConflict(t *testing.T) {
	_, err := New(WithHTTPClient(&http.Client{}), WithMaxIdleConns(10))
	if !errors.Is(err, ErrTransportConflict) {
		t.Fatalf("err = %v, want ErrTransportConflict", err)
	}
}

func TestNew_BYOComposesWithRetryOption(t *testing.T) {
	client, err := New(WithHTTPClient(&http.Client{Timeout: 3 * time.Second}), WithPerAttemptTimeout(time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.Timeout != 3*time.Second {
		t.Errorf("BYO client Timeout not preserved: %v", client.Timeout)
	}
	if _, ok := client.Transport.(*retryTransport); !ok {
		t.Error("transport was not wrapped in retryTransport")
	}
}

// NewSingleHost sizes all three pool dimensions to DefaultPoolSize, global >= per-host.
func TestNewSingleHost_PoolCoherence(t *testing.T) {
	client, err := NewSingleHost()
	if err != nil {
		t.Fatal(err)
	}
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
