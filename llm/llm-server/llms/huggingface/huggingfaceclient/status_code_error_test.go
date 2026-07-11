package huggingfaceclient

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStatusCodeError_Retryable verifies the transient/terminal classification:
// 5xx, 429, and 408 are retryable; other 4xx client errors (notably a 400
// context-length overflow) are terminal and must surface immediately so the
// caller's recovery can run.
func TestStatusCodeError_Retryable(t *testing.T) {
	cases := []struct {
		name string
		code int
		want bool
	}{
		{"400 context-length overflow → terminal", http.StatusBadRequest, false},
		{"401 unauthorized → terminal", http.StatusUnauthorized, false},
		{"404 not found → terminal", http.StatusNotFound, false},
		{"413 payload too large → terminal", http.StatusRequestEntityTooLarge, false},
		{"422 unprocessable → terminal", http.StatusUnprocessableEntity, false},
		{"408 request timeout → retryable", http.StatusRequestTimeout, true},
		{"429 too many requests → retryable", http.StatusTooManyRequests, true},
		{"500 internal error → retryable", http.StatusInternalServerError, true},
		{"503 unavailable → retryable", http.StatusServiceUnavailable, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, (&StatusCodeError{StatusCode: c.code}).Retryable())
		})
	}
}

// TestStatusCodeError_WrapsSentinel guards that the typed error still satisfies
// errors.Is(ErrUnexpectedStatusCode) and keeps the body for downstream matching.
func TestStatusCodeError_WrapsSentinel(t *testing.T) {
	err := error(&StatusCodeError{StatusCode: 400, Body: `maximum context length is 32768`})
	assert.True(t, errors.Is(err, ErrUnexpectedStatusCode))
	assert.Contains(t, err.Error(), "maximum context length")
	assert.Contains(t, err.Error(), "400")
}
