package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/etkecc/go-kit/retry"
)

// newStubTransport builds a retryTransport over a stub base for tests that control timing
// and outcomes without a real network. Jitter is off so honored delays are exact.
func newStubTransport(base http.RoundTripper, opts ...func(*retryTransport)) *retryTransport {
	rt := &retryTransport{
		base: base,
		retrier: retry.New(
			retry.WithMaxRetries(defaultMaxRetries),
			retry.WithDelayStep(defaultDelayStep),
			retry.WithJitter(false),
			retry.WithRetryIf(defaultRetryIf),
		),
		perAttempt:    DefaultPerAttemptTimeout,
		maxRetryAfter: DefaultMaxRetryAfter,
		budget:        noopBudget{},
	}
	for _, o := range opts {
		o(rt)
	}
	return rt
}

// denyBudget refuses every retry, exercising the budget gate.
type denyBudget struct{}

func (denyBudget) Allow() bool   { return false }
func (denyBudget) Record(_ bool) {}

func mustRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	return req
}

// The response body stays readable after RoundTrip returns; a defer cancel() would cancel
// the attempt context and poison this read.
func TestRoundTrip_BodyReadableAfterReturn(t *testing.T) {
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello world")
	}))
	client := NewSingleHost()

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read body after RoundTrip returned: %v", err)
	}
	if string(body) != "hello world" {
		t.Errorf("body = %q, want %q", body, "hello world")
	}
}

// Retries exhausted over a 500 return the terminal response live and readable, and the
// intermediate responses are drained so the connection is reused, not leaked. A leak would
// open a fresh connection per attempt and push the count past 1.
func TestRoundTrip_RetryExhaustionLiveAndReuses(t *testing.T) {
	srv, connCount := connCountingServer(t, statusSequenceHandler(500, 500, 500))
	client := NewSingleHost(WithRetry(retry.New(
		retry.WithMaxRetries(3),
		retry.WithDelayStep(time.Millisecond),
		retry.WithJitter(false),
		retry.WithRetryIf(defaultRetryIf),
	)))

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("want live 500, got error %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read terminal body: %v", err)
	}
	if len(body) == 0 {
		t.Error("terminal response body is empty")
	}
	if c := connCount(); c != 1 {
		t.Errorf("connections opened = %d, want 1 (intermediate 500s drained and reused, not leaked)", c)
	}
}

// A non-idempotent request that dies to a per-attempt timeout (a transport error, not a
// status) must not retry. The classifier is method-blind, so the gate lives in the
// transport; without it a POST would double-fire on every timeout.
func TestRoundTrip_NonIdempotentTimeoutNotRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		stub := stubTransport{fn: func(req *http.Request) (*http.Response, error) {
			attempts.Add(1)
			<-req.Context().Done()
			return nil, req.Context().Err()
		}}
		rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = time.Second })

		req := mustRequest(t, http.MethodPost, "http://x/charge", nil)
		resp, err := rt.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
			t.Error("expected nil response on error")
		}
		if err == nil {
			t.Fatal("expected a timeout error")
		}
		if n := attempts.Load(); n != 1 {
			t.Errorf("POST attempts on timeout = %d, want 1 (non-idempotent must not retry)", n)
		}
	})
}

// A non-idempotent request opts into retry with WithRetryNonIdempotent.
func TestRoundTrip_NonIdempotentOptInRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
			attempts.Add(1)
			return newResponse(http.StatusInternalServerError, "err"), nil
		}}
		rt := newStubTransport(stub, func(rt *retryTransport) {
			rt.nonIdem = true
			rt.perAttempt = 0
		})

		req := mustRequest(t, http.MethodPost, "http://x/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if n := attempts.Load(); n != defaultMaxRetries {
			t.Errorf("POST attempts with opt-in = %d, want %d", n, defaultMaxRetries)
		}
	})
}

// A non-idempotent request gets one attempt on a retryable status by default, and the
// status is returned live.
func TestRoundTrip_NonIdempotentStatusNotRetried(t *testing.T) {
	var attempts atomic.Int32
	stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return newResponse(http.StatusServiceUnavailable, "e"), nil
	}}
	rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = 0 })

	req := mustRequest(t, http.MethodPost, "http://x/", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 live", resp.StatusCode)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1", n)
	}
}

// A retryable request with a body and no GetBody can't be replayed.
func TestRoundTrip_NonReplayableBody(t *testing.T) {
	stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, ""), nil
	}}
	rt := newStubTransport(stub, func(rt *retryTransport) { rt.nonIdem = true })

	req := mustRequest(t, http.MethodPost, "http://x/", strings.NewReader("payload"))
	req.GetBody = nil // strings.Reader would otherwise supply one; force the non-replayable case

	resp, err := rt.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
		t.Error("expected nil response on error")
	}
	if !errors.Is(err, ErrNonReplayableBody) {
		t.Errorf("err = %v, want ErrNonReplayableBody", err)
	}
}

// A per-attempt timeout with the caller still alive is retryable, so a slow first attempt
// followed by a fast second succeeds in two attempts.
func TestRoundTrip_PerAttemptTimeoutRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		stub := stubTransport{fn: func(req *http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			return newResponse(http.StatusOK, "ok"), nil
		}}
		rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = time.Second })

		req := mustRequest(t, http.MethodGet, "http://x/", nil)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("want success on retry, got %v", err)
		}
		_ = resp.Body.Close()
		if n := attempts.Load(); n != 2 {
			t.Errorf("attempts = %d, want 2", n)
		}
	})
}

// A caller cancel mid-flight is terminal: one attempt, context error out.
func TestRoundTrip_CallerCancelMidFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		stub := stubTransport{fn: func(req *http.Request) (*http.Response, error) {
			attempts.Add(1)
			cancel()
			<-req.Context().Done()
			return nil, req.Context().Err()
		}}
		rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = 0 })

		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/", http.NoBody)
		if rerr != nil {
			t.Fatalf("build request: %v", rerr)
		}
		resp, err := rt.RoundTrip(req)
		if resp != nil {
			_ = resp.Body.Close()
			t.Error("expected nil response on error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		if n := attempts.Load(); n != 1 {
			t.Errorf("attempts = %d, want 1", n)
		}
	})
}

// A Retry-After within the cap is honored over the linear step. Jitter is off, so the wait
// equals the header exactly, distinct from the 200ms default step.
func TestRoundTrip_RetryAfterHonored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				r := newResponse(http.StatusServiceUnavailable, "slow down")
				r.Header.Set("Retry-After", "5")
				return r, nil
			}
			return newResponse(http.StatusOK, "ok"), nil
		}}
		rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = 0 })

		req := mustRequest(t, http.MethodGet, "http://x/", nil)
		start := time.Now()
		resp, err := rt.RoundTrip(req)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if elapsed != 5*time.Second {
			t.Errorf("waited %v, want 5s (Retry-After honored over the linear step)", elapsed)
		}
	})
}

// A Retry-After beyond the cap is not honored; the response returns live with no retry.
func TestRoundTrip_RetryAfterPastCapReturnsLive(t *testing.T) {
	var attempts atomic.Int32
	stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
		attempts.Add(1)
		r := newResponse(http.StatusServiceUnavailable, "much later")
		r.Header.Set("Retry-After", "3600")
		return r, nil
	}}
	rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = 0 })

	req := mustRequest(t, http.MethodGet, "http://x/", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 live", resp.StatusCode)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1 (Retry-After past cap must not retry)", n)
	}
}

// A Retry-After large enough to overflow int64 nanoseconds is treated as past the cap and
// returned live, never wrapped to a negative duration that would slip under the cap.
func TestRoundTrip_RetryAfterOverflowReturnsLive(t *testing.T) {
	var attempts atomic.Int32
	stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
		attempts.Add(1)
		r := newResponse(http.StatusServiceUnavailable, "later")
		r.Header.Set("Retry-After", "9999999999999")
		return r, nil
	}}
	rt := newStubTransport(stub, func(rt *retryTransport) { rt.perAttempt = 0 })

	req := mustRequest(t, http.MethodGet, "http://x/", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 live", resp.StatusCode)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1 (overflow Retry-After must not retry)", n)
	}
}

// The budget gate denies a retry before the second attempt runs.
func TestRoundTrip_BudgetDeniesRetry(t *testing.T) {
	var attempts atomic.Int32
	stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return newResponse(http.StatusInternalServerError, "e"), nil
	}}
	rt := newStubTransport(stub, func(rt *retryTransport) {
		rt.budget = denyBudget{}
		rt.perAttempt = 0
	})

	req := mustRequest(t, http.MethodGet, "http://x/", nil)
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
		t.Error("expected nil response on error")
	}
	if !errors.Is(err, errBudgetExhausted) {
		t.Errorf("err = %v, want errBudgetExhausted", err)
	}
	if n := attempts.Load(); n != 1 {
		t.Errorf("attempts = %d, want 1 (budget denies the retry)", n)
	}
}

// Backoff over a 500 with no Retry-After follows the linear step: three attempts, two
// sleeps of step*1 + step*2.
func TestRoundTrip_LinearBackoffTiming(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stub := stubTransport{fn: func(_ *http.Request) (*http.Response, error) {
			return newResponse(http.StatusInternalServerError, "e"), nil
		}}
		rt := &retryTransport{
			base: stub,
			retrier: retry.New(
				retry.WithMaxRetries(3),
				retry.WithDelayStep(100*time.Millisecond),
				retry.WithJitter(false),
				retry.WithRetryIf(defaultRetryIf),
			),
			maxRetryAfter: DefaultMaxRetryAfter,
			budget:        noopBudget{},
		}

		req := mustRequest(t, http.MethodGet, "http://x/", nil)
		start := time.Now()
		resp, err := rt.RoundTrip(req)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if want := 100*time.Millisecond + 200*time.Millisecond; elapsed != want {
			t.Errorf("backoff = %v, want %v (step*1 + step*2)", elapsed, want)
		}
	})
}

// WithMaxRetries and WithRetryDelayStep tune the built-in retrier rather than replacing it,
// so the 429/5xx classifier rides along: a 500 keeps trying up to the configured total, and a
// 401 still fails fast on the first response. If tuning had swapped the classifier for a bare
// "retry any error" one, the 401 would keep knocking the full attempt count instead of once.
func TestSingleHost_GranularRetryTuningKeepsClassifier(t *testing.T) {
	t.Run("5xx honors WithMaxRetries", func(t *testing.T) {
		var reqs atomic.Int32
		srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reqs.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		client := NewSingleHost(WithMaxRetries(4), WithRetryDelayStep(time.Millisecond))

		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("want live 500, got %v", err)
		}
		_ = resp.Body.Close()
		if n := reqs.Load(); n != 4 {
			t.Errorf("attempts = %d, want 4 (WithMaxRetries(4) = 4 total attempts)", n)
		}
	})

	t.Run("4xx still fails fast under tuning", func(t *testing.T) {
		var reqs atomic.Int32
		srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reqs.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		client := NewSingleHost(WithMaxRetries(5), WithRetryDelayStep(time.Millisecond))

		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("want live 401, got %v", err)
		}
		_ = resp.Body.Close()
		if n := reqs.Load(); n != 1 {
			t.Errorf("attempts = %d, want 1 (4xx terminal, classifier survived the tuning)", n)
		}
	})
}
