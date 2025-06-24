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
