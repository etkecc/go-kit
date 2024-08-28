package kit

import (
	"encoding/json"
	"io"
)

// ErrorResponse represents an error response
//
//nolint:errname // ErrorResponse is a valid name
type ErrorResponse struct {
	Err string `json:"error"`
}

// Error returns the error message
func (e ErrorResponse) Error() string {
	return e.Err
}

// NewErrorResponse creates a new error response
func NewErrorResponse(err error) ErrorResponse {
	if err == nil {
		return ErrorResponse{Err: "unknown error"}
	}

	return ErrorResponse{Err: err.Error()}
}

// MatrixError represents an error response from the Matrix API
type MatrixError struct {
	Code string `json:"errcode"`
	Err  string `json:"error"`
}

// Error returns the error message
func (e MatrixError) Error() string {
	return e.Err
}

// NewMatrixError creates a new Matrix error
func NewMatrixError(code, err string) MatrixError {
	return MatrixError{Code: code, Err: err}
}

// MatrixErrorFrom creates a new Matrix error from io.Reader
func MatrixErrorFrom(r io.Reader) MatrixError {
	var matrixErr MatrixError
	if err := json.NewDecoder(r).Decode(&matrixErr); err != nil {
		return NewMatrixError("M_UNKNOWN", err.Error())
	}

	return matrixErr
}
