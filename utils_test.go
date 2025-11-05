package kit

import (
	"testing"
	"unsafe"
)

// TestEq tests the Eq function for different scenarios
func TestEq(t *testing.T) {
	tests := []struct {
		s1       string
		s2       string
		expected bool
	}{
		// Identical strings
		{"hello", "hello", true},
		{"world", "world", true},
		{"", "", true},

		// Different strings
		{"hello", "world", false},
		{"hello", "helloo", false},
		{"world", "word", false},
		{"", " ", false},

		// Case sensitivity
		{"Hello", "hello", false},
		{"Case", "case", false},

		// Unicode characters
		{"こんにちは", "こんにちは", true},
		{"こんにちは", "こんにちは ", false},
		{"🚀", "🚀", true},
		{"🚀", "🚀🚀", false},

		// Empty string vs non-empty
		{"", "a", false},
		{"a", "", false},

		// Large strings
		{string(make([]byte, 1024)), string(make([]byte, 1024)), true}, // Both are same large strings

		// Strings with different encodings
		{"hello", "hello\x00", false},
		{"hello\x00", "hello", false},
	}

	for _, test := range tests {
		result := Eq(test.s1, test.s2)
		if result != test.expected {
			t.Errorf("Eq(%q, %q) = %v, want %v", test.s1, test.s2, result, test.expected)
		}
	}
}

type iface interface {
	X()
}

type impl struct{}

func (impl) X() {}

func TestIsNil(t *testing.T) {
	t.Parallel()

	nonNilPtr := func() any {
		x := 42
		return &x
	}

	type T struct{}

	tests := []struct {
		name string
		in   any
		want bool
	}{
		// Direct nils
		{"nil literal", nil, true},
		{"nil interface var", func() any { var i any = nil; return i }(), true}, //nolint:revive // testing nil interface

		// Pointers
		{"typed nil pointer", (*int)(nil), true},
		{"non-nil pointer", nonNilPtr(), false},

		// Interface holds nil pointer (edge this implementation handles)
		{"iface holds nil pointer", func() any { var p *T = nil; var i any = p; return i }(), true}, //nolint:revive // testing nil interface
		{"nested iface holds nil pointer", func() any {
			var p *T = nil //nolint:revive // testing nil pointer
			var i any = p
			var j any = i //nolint:all // testing nil interface
			return j
		}(), true},

		// Non-nil values
		{"int zero", 0, false},
		{"empty string", "", false},
		{"struct value", struct{}{}, false},
		{"iface holds non-nil concrete", iface(impl{}), false},

		// Other kinds that can be nil
		{"nil slice", []int(nil), true},
		{"nil map", map[string]int(nil), true},
		{"nil chan", (chan int)(nil), true}, //nolint:gocritic // testing nil chan
		{"nil func", (func())(nil), true},
		{"nil unsafe.Pointer", unsafe.Pointer(nil), true},
		{"iface holds nil map", func() any {
			var m map[string]int = nil //nolint:revive // testing nil map
			return any(m)
		}(), true},
		{"iface holds nil slice", func() any {
			var s []int = nil //nolint:revive // testing nil slice
			return any(s)
		}(), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := IsNil(tc.in)
			if got != tc.want {
				t.Fatalf("IsNil(%T) = %v, want %v (value: %#v)", tc.in, got, tc.want, tc.in)
			}
		})
	}
}
