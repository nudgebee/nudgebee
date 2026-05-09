package common

type Error struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type HasuraErrorAction struct {
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

func ErrorHasuraActionBadRequest(message string) HasuraErrorAction {
	return HasuraErrorAction{
		Message: message,
	}
}

// ErrorHasuraActionInternal returns a Hasura action error shape intended for
// 5xx responses. The payload format is identical to ErrorHasuraActionBadRequest
// (Hasura actions only surface `message`), but the helper name matches the
// HTTP status so call sites read correctly.
func ErrorHasuraActionInternal(message string) HasuraErrorAction {
	return HasuraErrorAction{
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
