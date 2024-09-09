package kit

import (
	"errors"
	"strings"
	"testing"
)

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
	errResp := NewErrorResponse(err)

	if errResp == nil {
		t.Fatal("NewErrorResponse() returned nil, expected non-nil")
	}

	if got := errResp.Err; got != "test error" {
		t.Errorf("NewErrorResponse().Err = %v, want %v", got, "test error")
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
