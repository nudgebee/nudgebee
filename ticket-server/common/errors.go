package common

// Error is an application error carrying a human-readable message and an
// associated HTTP status code.
type Error struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

// Error returns the error message, implementing the error interface.
func (e Error) Error() string {
	return e.Message
}

// ErrorBadRequest returns an Error with HTTP status code 400 (Bad Request).
func ErrorBadRequest(message string) Error {
	return Error{
		Message: message,
		Code:    400,
	}
}

// ErrorNotFound returns an Error with HTTP status code 404 (Not Found).
func ErrorNotFound(message string) Error {
	return Error{
		Message: message,
		Code:    404,
	}
}

// ErrorInternal returns an Error with HTTP status code 500 (Internal Server Error).
func ErrorInternal(message string) Error {
	return Error{
		Message: message,
		Code:    500,
	}
}
