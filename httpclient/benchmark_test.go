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
	client := NewSingleHost()
	drive(b, client, ts.URL)
}

// BenchmarkPoolSweep drives a widening working set of delegated hosts through NewMultiHost, all
// pinned to one latency-bearing backend via WithDialContext, so throughput reflects real fan-out
// reuse. It reports open FDs per host-count, making the multi-host idle caps' effect on socket
// pressure visible where the single-host 256-idle-per-host default would hoard.
func BenchmarkPoolSweep(b *testing.B) {
	ts := benchServer(10 * time.Millisecond)
	defer ts.Close()
	target := ts.Listener.Addr().String()

	for _, hosts := range []int{1, 16, 128, 1024} {
		b.Run(fmt.Sprintf("hosts-%d", hosts), func(b *testing.B) {
			client := NewMultiHost(WithDialContext(dialPinnedTo(target)))
			urls := make([]string, hosts)
			for i := range urls {
				urls[i] = fmt.Sprintf("http://h%d.test/", i)
			}
			driveHosts(b, client, urls)
			b.ReportMetric(float64(openFDs(b)), "open_fds")
		})
	}
}

// driveHosts is drive for a working set: parallel GETs round-robined across urls through one shared
// client, so pooling behaves as it would at real fan-out. Reports throughput; the caller reports FDs.
func driveHosts(b *testing.B, client *http.Client, urls []string) {
	b.Helper()
	ctx := context.Background()
	var total, idx uint64

	b.SetParallelism(32)
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	b.RunParallel(func(pb *testing.PB) {
		var local uint64
		for pb.Next() {
			reqURL := urls[atomic.AddUint64(&idx, 1)%uint64(len(urls))]
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
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
