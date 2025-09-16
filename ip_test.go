package kit

import (
	"testing"
)

func TestAnonymizeIP(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"hello", "hello"},
		{"192.168.1.42", "192.168.1.0"},
		{"8.8.8.8", "8.8.8.0"},
		{"255.255.255.255", "255.255.255.0"},
		{"0.0.0.0", "0.0.0.0"},
		{"192.168.1", "192.168.1"},
		{"::1", "::0"},
		{"2001:db8::1234", "2001:db8::0"},
		{"fe80::a6db:30ff:fe98:e946", "fe80::a6db:30ff:fe98:0"},
		{"2001:db8:abcd:0012:0000:0000:0000:0001", "2001:db8:abcd:12::0"},
		{"::", "::0"},
		{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::8a2e:370:0"},
		{"abcd::", "abcd::0"},
		{"1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:0"},
	}

	for i, tc := range tests {
		got := AnonymizeIP(tc.input)
		if got != tc.expected {
			t.Errorf("case %d: input=%q got=%q want=%q", i, tc.input, got, tc.expected)
		}
	}
}

func TestIsValidIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"", false},
		{" ", false},
		{"\t", false},
		{"\n", false},
		{"not an ip", false},
		{"1.2.3", false},
		{"1.2.3.4.5", false},
		{"256.0.0.1", false},
		{" 8.8.8.8", false},
		{"8.8.8.8 ", false},
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"100.64.0.1", true},
		{"240.0.0.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", false},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"169.254.1.1", false},
		{"224.0.0.1", false},
		{"::", false},
		{"::1", false},
		{"0:0:0:0:0:0:0:1", false},
		{"2001:4860:4860::8888", true},
		{"2001:db8::1", true},
		{"2001:DB8::1", true},
		{"fe80::1", false},
		{"fe80::", false},
		{"ff02::1", false},
		{"ff05::1", false},
		{"fc00::1", false},
		{"fd12:3456:789a::1", false},
		{"::ffff:8.8.8.8", true},
		{"::ffff:127.0.0.1", false},
		{"::ffff:192.168.1.1", false},
		{"::ffff:10.0.0.1", false},
		{"::ffff:169.254.1.1", false},
		{"fe80::1%eth0", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := IsValidIP(tt.ip); got != tt.want {
				t.Fatalf("IsValidIP(%q) = %v; want %v", tt.ip, got, tt.want)
			}
		})
	}
}
