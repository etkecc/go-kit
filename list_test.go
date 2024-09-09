package kit

import (
	"reflect"
	"testing"
)

// TestNewList tests the creation of a new list
func TestNewList(t *testing.T) {
	list := NewList[int, int]()

	if list == nil {
		t.Fatal("NewList() returned nil, expected non-nil")
	}

	if list.Len() != 0 {
		t.Errorf("NewList().Len() = %v, want %v", list.Len(), 0)
	}

	if list.mu == nil {
		t.Error("NewList() did not initialize the mutex")
	}
}

// TestNewListFrom tests the creation of a list from a slice
func TestNewListFrom(t *testing.T) {
	slice := []int{1, 2, 3, 4, 5}
	list := NewListFrom(slice)

	if list == nil {
		t.Fatal("NewListFrom() returned nil, expected non-nil")
	}

	if got, want := list.Len(), 5; got != want {
		t.Errorf("NewListFrom().Len() = %v, want %v", got, want)
	}

	// Ensure the list contains all the elements from the slice
	for _, v := range slice {
		if _, ok := list.data[v]; !ok {
			t.Errorf("NewListFrom() missing element %v", v)
		}
	}
}

// TestAdd tests adding a single item to the list
func TestAdd(t *testing.T) {
	list := NewList[int, int]()
	list.Add(42)

	if got, want := list.Len(), 1; got != want {
		t.Errorf("List.Len() = %v, want %v", got, want)
	}

	if _, ok := list.data[42]; !ok {
		t.Errorf("Add() did not add the element to the list")
	}
}

// TestAddSlice tests adding a slice of items to the list
func TestAddSlice(t *testing.T) {
	list := NewList[int, int]()
	slice := []int{1, 2, 3}
	list.AddSlice(slice)

	if got, want := list.Len(), len(slice); got != want {
		t.Errorf("AddSlice().Len() = %v, want %v", got, want)
	}

	// Ensure all elements from the slice are in the list
	for _, v := range slice {
		if _, ok := list.data[v]; !ok {
			t.Errorf("AddSlice() missing element %v", v)
		}
	}
}

// TestAddMapKeys tests adding map keys to the list
func TestAddMapKeys(t *testing.T) {
	list := NewList[int, string]()
	datamap := map[int]string{
		1: "one",
		2: "two",
		3: "three",
	}
	list.AddMapKeys(datamap)

	if got, want := list.Len(), len(datamap); got != want {
		t.Errorf("AddMapKeys().Len() = %v, want %v", got, want)
	}

	// Ensure all map keys are in the list
	for k := range datamap {
		if _, ok := list.data[k]; !ok {
			t.Errorf("AddMapKeys() missing key %v", k)
		}
	}
}

// TestRemove tests removing an item from the list
func TestRemove(t *testing.T) {
	list := NewList[int, int]()
	list.Add(42)
	list.Remove(42)

	if got, want := list.Len(), 0; got != want {
		t.Errorf("Remove().Len() = %v, want %v", got, want)
	}

	if _, ok := list.data[42]; ok {
		t.Errorf("Remove() did not remove the element from the list")
	}
}

// TestRemoveSlice tests removing a slice of items from the list
func TestRemoveSlice(t *testing.T) {
	list := NewList[int, int]()
	slice := []int{1, 2, 3, 4, 5}
	list.AddSlice(slice)
	list.RemoveSlice([]int{2, 4})

	if got, want := list.Len(), 3; got != want {
		t.Errorf("RemoveSlice().Len() = %v, want %v", got, want)
	}

	// Ensure that the remaining elements are correct
	for _, v := range []int{1, 3, 5} {
		if _, ok := list.data[v]; !ok {
			t.Errorf("RemoveSlice() missing remaining element %v", v)
		}
	}
}

// TestLen tests the Len method
func TestLen(t *testing.T) {
	list := NewList[int, int]()
	list.Add(42)

	if got, want := list.Len(), 1; got != want {
		t.Errorf("Len() = %v, want %v", got, want)
	}

	list.Remove(42)
	if got, want := list.Len(), 0; got != want {
		t.Errorf("Len() = %v, want %v", got, want)
	}
}

// TestSlice tests the Slice method for retrieving the list data
func TestSlice(t *testing.T) {
	list := NewList[int, int]()
	slice := []int{1, 2, 3}
	list.AddSlice(slice)

	result := list.Slice()
	expected := []int{1, 2, 3}

	// Sort and compare slices to account for order differences
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Slice() = %v, want %v", result, expected)
	}
}
