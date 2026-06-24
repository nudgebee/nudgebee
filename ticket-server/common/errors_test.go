package common

import (
	"errors"
	"testing"
)

func TestErrorMethod(t *testing.T) {
	tests := []struct {
		name string
		err  Error
		want string
	}{
		{name: "non-empty message", err: Error{Message: "boom", Code: 500}, want: "boom"},
		{name: "empty message", err: Error{Message: "", Code: 400}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorImplementsErrorInterface(t *testing.T) {
	var err error = ErrorBadRequest("bad input")
	if err.Error() != "bad input" {
		t.Errorf("Error() = %q, want %q", err.Error(), "bad input")
	}
}

func TestErrorConstructors(t *testing.T) {
	tests := []struct {
		name     string
		ctor     func(string) Error
		message  string
		wantCode int
	}{
		{name: "ErrorBadRequest", ctor: ErrorBadRequest, message: "bad request", wantCode: 400},
		{name: "ErrorNotFound", ctor: ErrorNotFound, message: "not found", wantCode: 404},
		{name: "ErrorInternal", ctor: ErrorInternal, message: "internal", wantCode: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ctor(tt.message)
			if got.Message != tt.message {
				t.Errorf("Message = %q, want %q", got.Message, tt.message)
			}
			if got.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", got.Code, tt.wantCode)
			}
			if got.Error() != tt.message {
				t.Errorf("Error() = %q, want %q", got.Error(), tt.message)
			}
		})
	}
}

func TestErrorConstructorsEmptyMessage(t *testing.T) {
	if got := ErrorBadRequest(""); got.Message != "" || got.Code != 400 {
		t.Errorf("ErrorBadRequest(\"\") = %+v, want {Message:\"\" Code:400}", got)
	}
}

func TestErrorIsComparable(t *testing.T) {
	// Error is a comparable value type, so errors.Is works for equal values.
	target := ErrorNotFound("missing")
	err := ErrorNotFound("missing")
	if !errors.Is(err, target) {
		t.Errorf("errors.Is(%+v, %+v) = false, want true", err, target)
	}
}
