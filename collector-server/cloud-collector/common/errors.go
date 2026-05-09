package common

type Error struct {
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
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
