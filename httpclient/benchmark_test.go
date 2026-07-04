package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// benchServer serves a fixed 4KiB payload, optionally after a per-request latency that
// stands in for real network RTT (loopback is near-zero and hides the value of pooling).
func benchServer(latency time.Duration) *httptest.Server {
	payload := []byte(strings.Repeat("x", 4096))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if latency > 0 {
			time.Sleep(latency)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
}

// drive runs GETs in parallel against url through client and reports throughput.
func drive(b *testing.B, client *http.Client, url string) {
	b.Helper()
	ctx := context.Background()
	var total uint64

	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	b.RunParallel(func(pb *testing.PB) {
		var local uint64
		for pb.Next() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			if err != nil {
				b.Error(err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			local++
		}
		atomic.AddUint64(&total, local)
	})
	elapsed := time.Since(start)
	b.ReportMetric(float64(total)/elapsed.Seconds(), "req/s")
}

// BenchmarkGetParallel is the baseline throughput and allocation profile against a
// zero-latency server.
func BenchmarkGetParallel(b *testing.B) {
	ts := benchServer(0)
	defer ts.Close()
	client, err := NewSingleHost()
	if err != nil {
		b.Fatal(err)
	}
	drive(b, client, ts.URL)
}

// BenchmarkPoolSweep sweeps the per-host connection pool against a latency-bearing server,
// so throughput climbs with pool size until connection reuse stops being the bottleneck.
// The chosen DefaultPoolSize is a floor: it should sit at or past where req/s plateaus.
func BenchmarkPoolSweep(b *testing.B) {
	ts := benchServer(10 * time.Millisecond)
	defer ts.Close()
	b.SetParallelism(32)

	for _, size := range []int{2, 16, 64, 256} {
		b.Run(fmt.Sprintf("pool-%d", size), func(b *testing.B) {
			client, err := NewSingleHost(
				WithMaxIdleConns(size),
				WithMaxIdleConnsPerHost(size),
				WithMaxConnsPerHost(size),
			)
			if err != nil {
				b.Fatal(err)
			}
			drive(b, client, ts.URL)
		})
	}
}
