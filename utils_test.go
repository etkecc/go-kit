package kit

import "testing"

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
