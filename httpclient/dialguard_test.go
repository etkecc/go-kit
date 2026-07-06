package httpclient

import (
	"context"
	"strings"
	"testing"
)

// dialGuard denies every private/mapped/smuggling/metadata range and allows public IPs. Addresses
// carry a port because that is the shape net.Dialer.Control receives.
func TestDialGuard_DeniesPrivateAllowsPublic(t *testing.T) {
	tests := []struct {
		addr   string
		denied bool
	}{
		{"127.0.0.1:80", true},
		{"10.0.0.1:80", true},
		{"192.168.1.1:80", true},
		{"169.254.169.254:80", true},
		{"[::1]:80", true},
		{"[fe80::1]:80", true},
		{"[fc00::1]:80", true},
		{"[::ffff:127.0.0.1]:80", true},
		{"[64:ff9b::a9fe:a9fe]:80", true},
		{"[2002:a9fe:a9fe::]:80", true},
		{"[2001::1]:80", true},
		{"[::169.254.169.254]:80", true},
		{"100.64.0.1:80", true},
		{"0.0.0.1:80", true},
		{"100.100.100.200:80", true},
		{"1.1.1.1:80", false},
		{"8.8.8.8:80", false},
		{"[2606:4700:4700::1111]:80", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			err := dialGuard("tcp", tt.addr, nil)
			if tt.denied && err == nil {
				t.Errorf("dialGuard(%q) allowed a disallowed address", tt.addr)
			}
			if !tt.denied && err != nil {
				t.Errorf("dialGuard(%q) refused a public address: %v", tt.addr, err)
			}
		})
	}
}

// A WithDialIP pin to loopback must die at the guard, checked on the transport's own DialContext so
// the assertion is the guard's refusal itself, not the retry layer's later reinterpretation of a
// blocked dial. The pin rewrites addr to the private IP, and Control has to fire on that rewritten
// address: this is the tripwire for the day the pinning wrapper gets moved onto an unguarded dialer.
func TestDialGuard_PinnedPrivateDialFails(t *testing.T) {
	tr := NewTransport(WithDialGuard())
	ctx := WithDialIP(context.Background(), "127.0.0.1")
	conn, err := tr.DialContext(ctx, "tcp", "example.com:80")
	if err == nil {
		_ = conn.Close()
		t.Fatal("pinned dial to 127.0.0.1 succeeded through the guard; the guard is not covering the pinned IP")
	}
	// "connection refused" would also be non-nil: assert the guard fired, not the kernel, so a
	// refactor that pins onto an unguarded dialer trips this instead of passing on a dead port.
	if !strings.Contains(err.Error(), "dial guard") {
		t.Fatalf("dial failed but not at the guard (%v); the pin may be bypassing Control", err)
	}
}
