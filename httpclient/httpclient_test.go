package httpclient

import (
	"crypto/tls"
	"io"
	"net"
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

// NewMultiHost trims per-host idle to 2 and idle timeout to 30s, leaves per-host concurrency
// unbounded and total idle at the shared default, and keeps global >= per-host.
func TestNewMultiHost_PoolCoherence(t *testing.T) {
	client := NewMultiHost()
	rt, ok := client.Transport.(*retryTransport)
	if !ok {
		t.Fatal("transport is not *retryTransport")
	}
	tr, ok := rt.base.(*http.Transport)
	if !ok {
		t.Fatal("base is not *http.Transport")
	}
	if tr.MaxIdleConnsPerHost != DefaultMultiHostIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", tr.MaxIdleConnsPerHost, DefaultMultiHostIdleConnsPerHost)
	}
	if tr.MaxIdleConns != DefaultPoolSize {
		t.Errorf("MaxIdleConns = %d, want %d", tr.MaxIdleConns, DefaultPoolSize)
	}
	if tr.MaxConnsPerHost != 0 {
		t.Errorf("MaxConnsPerHost = %d, want 0 (unbounded, or a wide fan-out serializes)", tr.MaxConnsPerHost)
	}
	if tr.IdleConnTimeout != DefaultMultiHostIdleConnTimeout {
		t.Errorf("IdleConnTimeout = %v, want %v", tr.IdleConnTimeout, DefaultMultiHostIdleConnTimeout)
	}
	if tr.MaxIdleConnsPerHost > tr.MaxIdleConns {
		t.Error("per-host idle pool exceeds the global idle pool")
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("TLS min version default is not TLS 1.2")
	}
}

// NewTransport builds a bare tuned transport: transport options apply, and its TransportOption
// parameter makes a retry knob (WithMaxRetries, WithOnAttempt) a compile error here, not a silent
// no-op, since NewTransport has no retry layer to configure.
func TestNewTransport_TransportOptionsApply(t *testing.T) {
	tr := NewTransport(WithMaxIdleConnsPerHost(9), WithIdleConnTimeout(7*time.Second))
	if tr.MaxIdleConnsPerHost != 9 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 9", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 7*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 7s", tr.IdleConnTimeout)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Error("expected the default TLS 1.2 floor")
	}
}

// TestMultiHost_DialPinningSafety is the load-bearing proof that the pool keys on the URL host,
// not the dialed IP. Negative half first (prove the gun is loaded): the naive IP-in-URL fix
// collapses two delegated hosts onto one TLS connection, so a request for b.test rides a conn
// authenticated as a.test. Positive half: WithDialContext pins the dial to the shared IP while the
// delegated host stays in the URL, and the two hosts keep distinct connections.
func TestMultiHost_DialPinningSafety(t *testing.T) {
	cert, roots := newMultiSANCert(t, "a.test", "b.test")
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	t.Run("negative_ip_in_url_collapses_two_hosts", func(t *testing.T) {
		srv, count := connCountingTLSServer(t, handler, &cert)
		// the naive fix: put the IP in the URL and set one SNI on the transport. the pool keys on
		// that IP, so both delegated hosts share one connection; the global SNI is a.test, so
		// b.test's request rides a conn TLS-authenticated as a.test. empty TLSNextProto forces
		// HTTP/1.1 so the count means "reused conn", not "multiplexed h2 stream".
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "a.test", MinVersion: tls.VersionTLS12},
			TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
		}
		client := &http.Client{Transport: tr}

		// SEQUENTIAL + drained: each conn returns to the idle pool (on the transport readLoop) before
		// the next request, so request 2 reuses request 1's conn and the collapse shows as count==1.
		// race that handoff and request 2 dials its own: count==2, and the witness spuriously fails.
		// reliable in practice; if a slow runner ever flakes it, barrier with a poll on conn state, no sleep.
		getDrain(t, client, srv.URL, "a.test")
		getDrain(t, client, srv.URL, "b.test")

		if got := count(); got != 1 {
			t.Fatalf("IP-in-URL must collapse both hosts onto one conn (that is the footgun), got %d", got)
		}
	})

	t.Run("positive_host_in_url_keeps_them_apart", func(t *testing.T) {
		srv, count := connCountingTLSServer(t, handler, &cert)
		_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
		if err != nil {
			t.Fatalf("split server addr: %v", err)
		}
		target := net.JoinHostPort("127.0.0.1", port)

		var attempts []AttemptInfo
		client := NewMultiHost(
			WithDialContext(dialPinnedTo(target)),
			WithOnAttempt(func(info AttemptInfo) { attempts = append(attempts, info) }),
		)
		// the library exposes no RootCAs knob (zero-config TLS by design), so reach into the base
		// transport the same way TestNewSingleHost_PoolCoherence does and trust the test cert. left
		// on the default protocols (h1+h2): this proves the shipping config keeps the two
		// authorities apart under h2, where cross-host coalescing would be the real danger.
		rt, ok := client.Transport.(*retryTransport)
		if !ok {
			t.Fatal("transport is not *retryTransport")
		}
		base, ok := rt.base.(*http.Transport)
		if !ok {
			t.Fatal("base is not *http.Transport")
		}
		base.TLSClientConfig.RootCAs = roots

		// first request also confirms h2 actually negotiated (verified on the wire), so "keeps
		// them apart under h2" is checked, not a silent h1 fallback. if Go ever implements the
		// cert-name coalescing its h2 pool still has as a TODO, count drops to 1 and this fails loud.
		if proto := getDrain(t, client, "https://a.test:"+port+"/", ""); proto != 2 {
			t.Fatalf("positive half must exercise HTTP/2 (the coalescing-danger path), got HTTP/%d", proto)
		}
		getDrain(t, client, "https://b.test:"+port+"/", "")
		if got := count(); got != 2 {
			t.Fatalf("distinct delegated hosts must open distinct conns, got %d", got)
		}

		// reuse is the whole point of pooling: a repeat of a.test rides its pooled conn, no third dial.
		getDrain(t, client, "https://a.test:"+port+"/", "")
		if got := count(); got != 2 {
			t.Fatalf("repeat host must reuse its pooled conn, got %d", got)
		}
		if len(attempts) == 0 || !attempts[len(attempts)-1].Reused {
			t.Fatalf("final attempt should report Reused=true, got %+v", attempts)
		}
	})
}

// TestMultiHost_PerHostIdleCapped is the regression guard for the per-host idle cap of 2, the knob
// that stops a wide crawl hoarding sockets. Two concurrent bursts to one host: the first opens a
// batch of conns, the cap trims the idle pool to 2, so the second burst reuses only those 2 and must
// dial the rest. A regression to the single-host 256 cap would pool the whole first burst and dial
// nothing on the second. Deterministic: WaitGroup sync and a connection counter, no FD timing.
func TestMultiHost_PerHostIdleCapped(t *testing.T) {
	const burst = 12
	srv, count := connCountingServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond) // hold each request so a burst overlaps into distinct conns
		_, _ = io.WriteString(w, "ok")
	}))
	client := NewMultiHost(WithDialContext(dialPinnedTo(srv.Listener.Addr().String())))

	fireConcurrent(t, client, "http://h.test/", burst)
	afterFirst := count()
	fireConcurrent(t, client, "http://h.test/", burst)
	newDials := count() - afterFirst

	// only 2 conns survive in the idle pool, so the second burst reuses 2 and dials the other ~10.
	// headroom (burst-4) absorbs any conn the first burst happened to reuse rather than open.
	if newDials < burst-4 {
		t.Fatalf("second burst dialed %d new conns, want >= %d (per-host idle cap of 2 regressed?)", newDials, burst-4)
	}
}
