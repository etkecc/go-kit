package httpclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestServer starts an httptest server closed at test end.
func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// newConnCounter returns a ConnState hook that tallies fresh TCP connections and a reader for
// the running total. Distinct-connection count is how the safety tests tell "pool collapsed
// two hosts onto one conn" (bad) from "kept them apart" (good).
func newConnCounter() (onState func(net.Conn, http.ConnState), count func() int) {
	var mu sync.Mutex
	total := 0
	return func(_ net.Conn, state http.ConnState) {
			if state == http.StateNew {
				mu.Lock()
				total++
				mu.Unlock()
			}
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return total
		}
}

// connCountingServer starts a server that counts distinct client connections, so a test
// can assert connections are reused across retries rather than leaked.
func connCountingServer(t *testing.T, handler http.Handler) (srv *httptest.Server, count func() int) {
	t.Helper()
	onState, count := newConnCounter()
	srv = httptest.NewUnstartedServer(handler)
	srv.Config.ConnState = onState
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, count
}

// connCountingTLSServer is connCountingServer over TLS with a caller-supplied cert, for the
// multi-host safety tests that need real SNI and cert validation against the delegated host.
func connCountingTLSServer(t *testing.T, handler http.Handler, cert *tls.Certificate) (srv *httptest.Server, count func() int) {
	t.Helper()
	onState, count := newConnCounter()
	srv = httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true // so the safe path can prove h2 keeps two authorities apart, not just h1
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	srv.Config.ConnState = onState
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, count
}

// newMultiSANCert self-signs one cert covering every name in names plus 127.0.0.1, and the
// RootCAs pool that trusts it. One cert for both delegated hosts is what lets the safe path
// validate a.test and b.test against the same test backend without wiring per-host certs.
func newMultiSANCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "httpclient-test"},
		DNSNames:     names,
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, pool
}

// fireConcurrent launches n GETs to url through client at once and waits for all to finish with
// their bodies drained. Errors go through t.Errorf, safe from the spawned goroutines (t.Fatal is not).
func fireConcurrent(t *testing.T, client *http.Client, url string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
			if err != nil {
				t.Errorf("new request: %v", err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("do: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
}

// openFDs counts the process's open file descriptors via /proc, the observable proxy for how
// many sockets the pool is holding. Skips where /proc is absent, so it's a Linux-only signal.
func openFDs(tb testing.TB) int {
	tb.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		tb.Skipf("open-fd count needs /proc: %v", err)
	}
	return len(entries)
}

// dialPinnedTo returns a DialContext that ignores addr and always dials target, the safe
// shape WithDialContext demands: the delegated host stays in the URL, only the socket moves.
func dialPinnedTo(target string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, target)
	}
}

// getDrain issues one GET (overriding the Host header when host is set), drains and closes the
// body so the connection lands back in the idle pool, and returns the negotiated ProtoMajor.
// Draining is load-bearing for the safety tests: an undrained body pins the conn out of the pool
// and fakes a second dial. The proto return lets a caller confirm h2 actually negotiated.
func getDrain(t *testing.T, client *http.Client, url, host string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s (host %q): %v", url, host, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.ProtoMajor
}

// statusSequenceHandler returns each code in order, one per request, clamping to the last
// for any further requests, with a short body so keep-alive can reuse the connection.
func statusSequenceHandler(codes ...int) http.HandlerFunc {
	var n atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		code := codes[len(codes)-1]
		if i < len(codes) {
			code = codes[i]
		}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, "body"+strconv.Itoa(i))
	}
}

// stubTransport is a base RoundTripper for synctest-driven tests that must control timing
// without a real network.
type stubTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (s stubTransport) RoundTrip(req *http.Request) (*http.Response, error) { return s.fn(req) }

// newResponse builds a minimal readable response for stub transports.
func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
