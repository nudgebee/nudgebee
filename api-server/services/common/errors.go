package common

import (
	"errors"

	"github.com/lib/pq"
)

type Error struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type ErrorAction struct {
	Message string `json:"message,omitempty"`
}

func (e Error) Error() string {
	return e.Message
}

func ErrorBadRequest(message string) Error {
	return Error{
		Message: message,
		Code:    400,
	}
}

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

// ErrorActionForbidden returns a RPC action error shape intended for 403
// responses (the caller's role is valid but it lacks access to the
// requested account). Payload format matches ErrorActionBadRequest; the
// helper name matches the HTTP status so call sites read correctly.
func ErrorActionForbidden(message string) ErrorAction {
	return ErrorAction{
		Message: message,
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

func ErrorConflict(message string) Error {
	return Error{
		Message: message,
		Code:    409,
	}
}

// ErrorRefusedOr returns the database's own message, as a 400, when it refused a
// write with a CHECK rule (SQLSTATE 23514) — e.g. a bee account given a built-in
// role or a user group — so the caller learns why. Any other error becomes a 500
// with the fallback message.
func ErrorRefusedOr(err error, fallback string) Error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23514" {
		return ErrorBadRequest(pqErr.Message)
	}
	return ErrorInternal(fallback)
}
