package kit

import (
	"reflect"
	"testing"
)

func TestMapFromSlice(t *testing.T) {
	slice := []int{1, 2, 3, 2, 1}

	expected := map[int]bool{
		1: true,
		2: true,
		3: true,
	}
	result := MapFromSlice(slice)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("MapFromSlice() = %v, want %v", result, expected)
	}
}

// TestMapKeys tests the MapKeys function
func TestMapKeys(t *testing.T) {
	data := map[int]string{
		3: "three",
		1: "one",
		2: "two",
	}

	expected := []int{1, 2, 3}
	result := MapKeys(data)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("MapKeys() = %v, want %v", result, expected)
	}
}

// TestMapKeysEmptyMap tests MapKeys with an empty map
func TestMapKeysEmptyMap(t *testing.T) {
	data := map[int]string{}

	result := MapKeys(data)

	if len(result) != 0 {
		t.Errorf("MapKeys(empty map) = %v, want empty slice", result)
	}
}

// TestMergeMapKeys tests the MergeMapKeys function with multiple maps
func TestMergeMapKeys(t *testing.T) {
	m1 := map[string]int{
		"a": 1,
		"b": 2,
	}
	m2 := map[string]int{
		"b": 3,
		"c": 4,
	}
	m3 := map[string]int{
		"d": 5,
	}

	expected := []string{"a", "b", "c", "d"}
	result := MergeMapKeys(m1, m2, m3)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("MergeMapKeys() = %v, want %v", result, expected)
	}
}

// TestMergeMapKeysSingleMap tests MergeMapKeys with only one map
func TestMergeMapKeysSingleMap(t *testing.T) {
	m := map[string]int{
		"a": 1,
		"b": 2,
		"c": 3,
	}

	expected := []string{"a", "b", "c"}
	result := MergeMapKeys(m)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("MergeMapKeys(single map) = %v, want %v", result, expected)
	}
}

// TestMergeMapKeysEmptyMap tests MergeMapKeys with empty maps
func TestMergeMapKeysEmptyMap(t *testing.T) {
	m1 := map[string]int{}
	m2 := map[string]int{}
	m3 := map[string]int{}

	result := MergeMapKeys(m1, m2, m3)

	if len(result) != 0 {
		t.Errorf("MergeMapKeys(empty maps) = %v, want empty slice", result)
	}
}
