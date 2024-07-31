package kit

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
