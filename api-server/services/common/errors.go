package common

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
