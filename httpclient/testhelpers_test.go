package httpclient

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// newTestServer starts an httptest server closed at test end.
func newTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// connCountingServer starts a server that counts distinct client connections, so a test
// can assert connections are reused across retries rather than leaked.
func connCountingServer(t *testing.T, handler http.Handler) (srv *httptest.Server, count func() int) {
	t.Helper()
	var mu sync.Mutex
	total := 0
	srv = httptest.NewUnstartedServer(handler)
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			total++
			mu.Unlock()
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, func() int {
		mu.Lock()
		defer mu.Unlock()
		return total
	}
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
