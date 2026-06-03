package common

import "errors"

type Error struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

// ErrorAction is the response shape RPC actions expect for errors.
// Using it prevents RPC from wrapping our payload and leaking internal
// structure; only the `message` field is surfaced to the caller.
type ErrorAction struct {
	Message string `json:"message,omitempty"`
}

func (e Error) Error() string {
	return e.Message
}

// ErrorActionBadRequest returns a RPC-action-shaped error for 4xx responses.
func ErrorActionBadRequest(message string) ErrorAction {
	return ErrorAction{
		Message: message,
	}
}

// ErrorActionInternal returns a RPC action error shape intended for
// 5xx responses. The payload format is identical to ErrorActionBadRequest
// (RPC actions only surface `message`), but the helper name matches the
// HTTP status so call sites read correctly.
func ErrorActionInternal(message string) ErrorAction {
	return ErrorAction{
		Message: message,
	}
}

func ErrorBadRequest(message string) Error {
	return Error{
		Message: message,
		Code:    400,
	}
}

func ErrorUnauthorized(message string) Error {
	return Error{
		Message: message,
		Code:    403,
	}
}

func ErrorUnauthenticated(message string) Error {
	return Error{
		Message: message,
		Code:    401,
	}
}

func ErrorNotFound(message string) Error {
	return Error{
		Message: message,
		Code:    404,
	}
}

func ErrorInternal(message string) Error {
	return Error{
		Message: message,
		Code:    500,
	}
}

var ErrWorkerPoolTimeout = errors.New("worker pool submission timed out")
