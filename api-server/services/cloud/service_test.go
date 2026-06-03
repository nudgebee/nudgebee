package cloud

import (
	"errors"
	"testing"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		want       bool
	}{
		{"network error retries", errors.New("connection refused"), 0, true},
		{"429 retries", nil, 429, true},
		{"500 does not retry", nil, 500, false},
		{"502 does not retry", nil, 502, false},
		{"503 does not retry", nil, 503, false},
		{"504 does not retry", nil, 504, false},
		{"400 does not retry", nil, 400, false},
		{"200 does not retry", nil, 200, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err, tt.statusCode); got != tt.want {
				t.Errorf("isRetryableError(%v, %d) = %v, want %v", tt.err, tt.statusCode, got, tt.want)
			}
		})
	}
}
