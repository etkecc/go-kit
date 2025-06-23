package kit

import (
	"reflect"
	"strings"
	"testing"
)

// TestTruncate tests the Truncate function with a variety of cases
func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		length   int
		expected string
	}{
		// Standard cases
		{"hello world", 5, "hello..."},
		{"hello", 10, "hello"},
		{"привет мир", 6, "привет..."},
		{"прiвет свiт", 6, "прiвет..."},

		// Edge cases
		{"hello world", 0, ""},
		{"hello world", -1, ""},
		{"hello world", 11, "hello world"},
		{"hello world", 20, "hello world"},
		{"hello world", 1, "h..."},
		{"hello world", 2, "he..."},
		{"hello world", 3, "hel..."},
		{"hello world", 4, "hell..."},
		{"hello world", 5, "hello..."},
		{"hello world", 6, "hello ..."},
		{"hello world", 7, "hello w..."},
		{"hello world", 8, "hello wo..."},
		{"hello world", 9, "hello wor..."},
		{"hello world", 10, "hello worl..."},
		{"hello world", 11, "hello world"},
		{"こんにちは世界", 1, "こ..."},    //nolint:gosmopolitan // test
		{"こんにちは世界", 2, "こん..."},   //nolint:gosmopolitan // test
		{"こんにちは世界", 3, "こんに..."},  //nolint:gosmopolitan // test
		{"こんにちは世界", 9, "こんにちは世界"}, //nolint:gosmopolitan // test

		// Empty string and single character cases
		{"", 1, ""},
		{"a", 0, ""},
		{"a", 1, "a"},
		{"a", 2, "a"},
		{"a", 3, "a"},
		{"ab", 1, "a..."},
		{"ab", 2, "ab"},
		{"ab", 3, "ab"},

		// Unicode and multi-byte cases
		{"🌟", 1, "🌟"},
		{"🌟🌟", 1, "🌟..."},
		{"🌟🌟", 2, "🌟🌟"},
		{"🌟🌟🌟", 2, "🌟🌟..."},
		{"🌟🌟🌟", 3, "🌟🌟🌟"},
	}

	for _, test := range tests {
		result := Truncate(test.input, test.length)
		if result != test.expected {
			t.Errorf("Truncate(%q, %d) = %q, want %q", test.input, test.length, result, test.expected)
		}
	}
}

// TestUnquote tests the Unquote function
func TestUnquote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"hello"`, "hello"},
		{"hello", "hello"},
		{`"invalid`, `"invalid`}, // should return original string if unquoting fails
	}

	for _, test := range tests {
		result := Unquote(test.input)
		if result != test.expected {
			t.Errorf("Unquote(%q) = %q, want %q", test.input, result, test.expected)
		}
	}
}

// TestHash tests the Hash function
func TestHash(t *testing.T) {
	input := "hello"
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	result := Hash(input)
	if result != expected {
		t.Errorf("Hash(%q) = %q, want %q", input, result, expected)
	}
}

// TestStringToInt tests the StringToInt function
func TestStringToInt(t *testing.T) {
	tests := []struct {
		input        string
		defaultValue []int
		expected     int
	}{
		{"42", nil, 42},
		{"not an int", nil, 0},
		{"not an int", []int{10}, 10},
		{"100", []int{10}, 100},
	}

	for _, test := range tests {
		result := StringToInt(test.input, test.defaultValue...)
		if result != test.expected {
			t.Errorf("StringToInt(%q, %v) = %d, want %d", test.input, test.defaultValue, result, test.expected)
		}
	}
}

// TestStringToSlice tests the StringToSlice function
func TestStringToSlice(t *testing.T) {
	tests := []struct {
		input        string
		defaultValue []string
		expected     []string
	}{
		{"a,b,c", nil, []string{"a", "b", "c"}},
		{" a , b , c ", nil, []string{"a", "b", "c"}},
		{"", []string{"default"}, []string{"default"}},
	}

	for _, test := range tests {
		result := StringToSlice(test.input, test.defaultValue...)
		if !reflect.DeepEqual(result, test.expected) {
			t.Errorf("StringToSlice(%q, %v) = %v, want %v", test.input, test.defaultValue, result, test.expected)
		}
	}
}

// TestSliceToString tests the SliceToString function
func TestSliceToString(t *testing.T) {
	slice := []string{"a", "b", "c"}

	tests := []struct {
		delimiter string
		hook      func(string) string
		expected  string
	}{
		{",", nil, "a,b,c"},
		{",", strings.ToUpper, "A,B,C"},
	}

	for _, test := range tests {
		result := SliceToString(slice, test.delimiter, test.hook)
		if result != test.expected {
			t.Errorf("SliceToString(%v, %q, hook) = %q, want %q", slice, test.delimiter, result, test.expected)
		}
	}
}
