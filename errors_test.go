package kit

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

func newErr(t *testing.T, msg string) error {
	t.Helper()
	return errors.New(msg)
}

func TestAggregateError_Error_Empty(t *testing.T) {
	var a AggregateError
	if got := a.Error(); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestAggregateError_Error_WithErrors(t *testing.T) {
	a := &AggregateError{
		Errors: []error{
			newErr(t, "first"),
			newErr(t, "second"),
			newErr(t, "third"),
		},
	}
	got := a.Error()
	parts := strings.Split(got, "; ")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), got)
	}
	if parts[0] != "first" || parts[1] != "second" || parts[2] != "third" {
		t.Fatalf("unexpected messages in Error(): %q", got)
	}
}

func TestAggregateError_Unwrap_Empty(t *testing.T) {
	var a AggregateError
	if got := a.Unwrap(); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestAggregateError_Unwrap_NonEmpty(t *testing.T) {
	errs := []error{newErr(t, "a"), newErr(t, "b")}
	a := &AggregateError{Errors: errs}

	got := a.Unwrap()
	if got == nil {
		t.Fatalf("expected non-nil slice")
	}
	if len(got) != len(errs) {
		t.Fatalf("expected %d errors, got %d", len(errs), len(got))
	}
	// ensure it is the same underlying slice (as documented)
	if &got[0] != &errs[0] {
		t.Fatalf("expected underlying slice to be identical")
	}
}

func TestAggregateError_Is(t *testing.T) {
	target := newErr(t, "target")
	wrapped := fmt.Errorf("wrapped: %w", target)
	a := &AggregateError{
		Errors: []error{
			newErr(t, "other"),
			wrapped,
		},
	}

	if !a.Is(target) {
		t.Fatalf("expected Is to find target error")
	}

	if a.Is(newErr(t, "nonexistent")) {
		t.Fatalf("expected Is to return false for missing error")
	}
}

func TestAggregateError_As(t *testing.T) {
	ce := &customError{"x"}
	a := &AggregateError{
		Errors: []error{
			newErr(t, "other"),
			ce,
		},
	}

	var target *customError
	if !a.As(&target) {
		t.Fatalf("expected As to find customErr")
	}
	if target != ce {
		t.Fatalf("expected target to point to original customErr")
	}

	var notFound *MatrixError
	if a.As(&notFound) {
		t.Fatalf("expected As to return false for unmatched type")
	}
}

func TestAggregateError_Join_NilReceiver(t *testing.T) {
	var a *AggregateError
	// Calling method on nil receiver should panic; verify behavior is defined.
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on Join with nil receiver")
		}
	}()
	_ = a.Join(newErr(t, "x"))
}

func TestAggregateError_Join_FilterNilAndReturnNilWhenEmpty(t *testing.T) {
	a := &AggregateError{}
	if got := a.Join(nil, nil); got != nil {
		t.Fatalf("expected nil when only nil errors added, got %#v", got)
	}
	if a.Errors == nil {
		t.Fatalf("expected internal slice to be initialized even when returning nil")
	}
	if len(a.Errors) != 0 {
		t.Fatalf("expected internal slice to remain empty, got %d", len(a.Errors))
	}
}

func TestAggregateError_Join_AddsErrorsAndReturnsSelf(t *testing.T) {
	a := &AggregateError{}
	e1 := newErr(t, "one")
	e2 := newErr(t, "two")

	got := a.Join(nil, e1, nil, e2)
	if got != a {
		t.Fatalf("expected Join to return receiver, got %#v", got)
	}
	if len(a.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(a.Errors))
	}
	if !errors.Is(a.Errors[0], e1) || !errors.Is(a.Errors[1], e2) {
		t.Fatalf("unexpected errors slice: %#v", a.Errors)
	}
}

func TestAggregateError_Join_Concurrent(t *testing.T) {
	const goroutines = 10
	const perGoroutine = 50

	a := &AggregateError{}
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				a.Join(newErr(t, fmt.Sprintf("g%d-%d", id, i)))
			}
		}(g)
	}

	wg.Wait()

	// We don't check exact messages (they're not important) but ensure no data race & count
	if len(a.Errors) != goroutines*perGoroutine {
		t.Fatalf("expected %d errors, got %d", goroutines*perGoroutine, len(a.Errors))
	}
}

func TestNewAggregateError_NoErrorsReturnsNil(t *testing.T) {
	if got := NewAggregateError(); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
	if got := NewAggregateError(nil, nil); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}
}

func TestNewAggregateError_WithErrors(t *testing.T) {
	e1 := newErr(t, "one")
	e2 := newErr(t, "two")

	got := NewAggregateError(nil, e1, e2)
	if got == nil {
		t.Fatalf("expected non-nil aggregate")
	}
	if len(got.Errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(got.Errors))
	}
	if !errors.Is(got.Errors[0], e1) || !errors.Is(got.Errors[1], e2) {
		t.Fatalf("unexpected errors slice: %#v", got.Errors)
	}
}

// TestErrorResponseError tests the Error method of ErrorResponse
func TestErrorResponseError(t *testing.T) {
	errResp := ErrorResponse{Err: "something went wrong"}

	if got := errResp.Error(); got != "something went wrong" {
		t.Errorf("ErrorResponse.Error() = %v, want %v", got, "something went wrong")
	}
}

// TestNewErrorResponse tests the NewErrorResponse function
func TestNewErrorResponse(t *testing.T) {
	err := errors.New("test error")
	errResp := NewErrorResponse(err, http.StatusBadGateway)

	if errResp == nil {
		t.Fatal("NewErrorResponse() returned nil, expected non-nil")
	}

	if got := errResp.Err; got != "test error" {
		t.Errorf("NewErrorResponse().Err = %v, want %v", got, "test error")
	}

	if got := errResp.StatusCode; got != http.StatusBadGateway {
		t.Errorf("NewErrorResponse().StatusCode = %v, want %v", got, http.StatusBadGateway)
	}
}

// TestNewErrorResponseNilError tests NewErrorResponse when the error is nil
func TestNewErrorResponseNilError(t *testing.T) {
	errResp := NewErrorResponse(nil)

	if errResp == nil {
		t.Fatal("NewErrorResponse(nil) returned nil, expected non-nil")
	}

	if got := errResp.Err; got != "unknown error" {
		t.Errorf("NewErrorResponse(nil).Err = %v, want %v", got, "unknown error")
	}
}

// TestMatrixErrorError tests the Error method of MatrixError
func TestMatrixErrorError(t *testing.T) {
	matrixErr := MatrixError{Err: "Matrix error"}

	if got := matrixErr.Error(); got != "Matrix error" {
		t.Errorf("MatrixError.Error() = %v, want %v", got, "Matrix error")
	}
}

// TestNewMatrixError tests the NewMatrixError function
func TestNewMatrixError(t *testing.T) {
	code := "M_UNKNOWN"
	message := "Something went wrong"
	matrixErr := NewMatrixError(code, message)

	if matrixErr == nil {
		t.Fatal("NewMatrixError() returned nil, expected non-nil")
	}

	if got := matrixErr.Code; got != code {
		t.Errorf("NewMatrixError().Code = %v, want %v", got, code)
	}

	if got := matrixErr.Err; got != message {
		t.Errorf("NewMatrixError().Err = %v, want %v", got, message)
	}
}

// TestMatrixErrorFromSuccess tests MatrixErrorFrom with a valid JSON response
func TestMatrixErrorFromSuccess(t *testing.T) {
	jsonStr := `{"errcode":"M_FORBIDDEN", "error":"Forbidden access"}`
	reader := strings.NewReader(jsonStr)

	matrixErr := MatrixErrorFrom(reader)

	if matrixErr == nil {
		t.Fatal("MatrixErrorFrom() returned nil, expected non-nil")
	}

	if got := matrixErr.Code; got != "M_FORBIDDEN" {
		t.Errorf("MatrixErrorFrom().Code = %v, want %v", got, "M_FORBIDDEN")
	}

	if got := matrixErr.Err; got != "Forbidden access" {
		t.Errorf("MatrixErrorFrom().Err = %v, want %v", got, "Forbidden access")
	}
}

// TestMatrixErrorFromInvalidJSON tests MatrixErrorFrom with an invalid JSON response
func TestMatrixErrorFromInvalidJSON(t *testing.T) {
	jsonStr := `{"errcode":"M_FORBIDDEN", "error":"Forbidden access"` // malformed JSON
	reader := strings.NewReader(jsonStr)

	matrixErr := MatrixErrorFrom(reader)

	if matrixErr == nil {
		t.Fatal("MatrixErrorFrom() returned nil, expected non-nil")
	}

	if got := matrixErr.Code; got != "M_UNKNOWN" {
		t.Errorf("MatrixErrorFrom().Code = %v, want %v", got, "M_UNKNOWN")
	}

	if !strings.Contains(matrixErr.Err, "failed to decode error response") {
		t.Errorf("MatrixErrorFrom().Err = %v, want it to contain 'failed to decode error response'", matrixErr.Err)
	}
}

// TestMatrixErrorFromNilReader tests MatrixErrorFrom with a nil reader
func TestMatrixErrorFromNilReader(t *testing.T) {
	matrixErr := MatrixErrorFrom(nil)

	if matrixErr != nil {
		t.Errorf("MatrixErrorFrom(nil) = %v, want nil", matrixErr)
	}
}

// TestMatrixErrorFromEmptyReader tests MatrixErrorFrom with an empty reader
func TestMatrixErrorFromEmptyReader(t *testing.T) {
	reader := strings.NewReader("")
	expected := `failed to decode error response "": unexpected end of JSON input`

	matrixErr := MatrixErrorFrom(reader)

	if matrixErr.Error() != expected {
		t.Errorf("MatrixErrorFrom(empty reader) = %v, want `%s`", matrixErr, expected)
	}
}
