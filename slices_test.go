package kit

import (
	"reflect"
	"testing"
)

// TestUniq tests the Uniq function
func TestUniq(t *testing.T) {
	slice := []string{"a", "b", "a", "c", "b"}
	expected := []string{"a", "b", "c"}
	result := Uniq(slice)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Uniq() = %v, want %v", result, expected)
	}
}

// TestUniqEmptySlice tests Uniq with an empty slice
func TestUniqEmptySlice(t *testing.T) {
	slice := []string{}
	expected := []string{}
	result := Uniq(slice)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Uniq(empty slice) = %v, want %v", result, expected)
	}
}

// TestMergeSlices tests the MergeSlices function with multiple slices
func TestMergeSlices(t *testing.T) {
	slice1 := []int{1, 2, 3}
	slice2 := []int{3, 4, 5}
	slice3 := []int{5, 6, 7}

	expected := []int{1, 2, 3, 4, 5, 6, 7}
	result := MergeSlices(slice1, slice2, slice3)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("MergeSlices() = %v, want %v", result, expected)
	}
}

// TestMergeSlicesEmpty tests MergeSlices with empty slices
func TestMergeSlicesEmpty(t *testing.T) {
	result := MergeSlices([]int{}, []int{})

	if len(result) != 0 {
		t.Errorf("MergeSlices(empty slices) = %v, want empty slice", result)
	}
}

// TestRemoveFromSlice tests the RemoveFromSlice function
func TestRemoveFromSlice(t *testing.T) {
	base := []int{1, 2, 3, 4, 5}
	toRemove := []int{2, 4}

	expected := []int{1, 3, 5}
	result := RemoveFromSlice(base, toRemove)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("RemoveFromSlice() = %v, want %v", result, expected)
	}
}

// TestRemoveFromSliceEmpty tests RemoveFromSlice with an empty base slice
func TestRemoveFromSliceEmpty(t *testing.T) {
	result := RemoveFromSlice([]int{}, []int{1, 2, 3})

	if len(result) != 0 {
		t.Errorf("RemoveFromSlice(empty base slice) = %v, want empty slice", result)
	}
}

// TestChunk tests the Chunk function
func TestChunk(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	chunkSize := 3

	expected := [][]int{{1, 2, 3}, {4, 5, 6}, {7, 8}}
	result := Chunk(items, chunkSize)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Chunk() = %v, want %v", result, expected)
	}
}

// TestChunkExact tests Chunk when items are evenly divisible by chunkSize
func TestChunkExact(t *testing.T) {
	items := []int{1, 2, 3, 4, 5, 6}
	chunkSize := 2

	expected := [][]int{{1, 2}, {3, 4}, {5, 6}}
	result := Chunk(items, chunkSize)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Chunk(exact division) = %v, want %v", result, expected)
	}
}

// TestChunkSingleElement tests Chunk when chunk size is larger than the number of items
func TestChunkSingleElement(t *testing.T) {
	items := []int{1}
	chunkSize := 3

	expected := [][]int{{1}}
	result := Chunk(items, chunkSize)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Chunk(single element) = %v, want %v", result, expected)
	}
}

// TestChunkZeroSize tests Chunk when chunk size is zero
func TestChunkZeroSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Chunk() did not panic when chunkSize was zero")
		}
	}()
	Chunk([]int{1, 2, 3}, 0)
}

// TestReverse tests the Reverse function
func TestReverse(t *testing.T) {
	// Test cases
	tests := []struct {
		name     string
		input    []int
		expected []int
	}{
		{"Empty slice", []int{}, []int{}},
		{"Single element", []int{1}, []int{1}},
		{"Two elements", []int{1, 2}, []int{2, 1}},
		{"Three elements", []int{1, 2, 3}, []int{3, 2, 1}},
		{"Four elements", []int{1, 2, 3, 4}, []int{4, 3, 2, 1}},
		{"Five elements", []int{1, 2, 3, 4, 5}, []int{5, 4, 3, 2, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy the input to avoid modifying the test case's input directly
			inputCopy := make([]int, len(tt.input))
			copy(inputCopy, tt.input)

			// Reverse the slice
			Reverse(inputCopy)

			// Check if the reversed slice matches the expected output
			if !reflect.DeepEqual(inputCopy, tt.expected) {
				t.Errorf("Reverse(%v) = %v, want %v", tt.input, inputCopy, tt.expected)
			}
		})
	}
}
