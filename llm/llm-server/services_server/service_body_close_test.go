package services_server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type trackingReadCloser struct {
	*strings.Reader
	closed bool
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}

func TestReadAndCloseServicesResponseBody(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("upstream exploded")}
	got, err := readAndCloseServicesResponseBody(&http.Response{Body: body}, "services_server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "upstream exploded" {
		t.Fatalf("body = %q, want upstream exploded", string(got))
	}
	if !body.closed {
		t.Fatal("expected response body to be closed")
	}
}

func TestServicesHTTPStatusError(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		wantErr     bool
		wantContain string
		wantOmit    string
	}{
		{
			name:       "success",
			statusCode: http.StatusNoContent,
		},
		{
			name:        "unauthorized includes upstream body",
			statusCode:  http.StatusUnauthorized,
			body:        "token expired",
			wantErr:     true,
			wantContain: "token expired",
		},
		{
			name:        "other client error includes upstream body",
			statusCode:  http.StatusTeapot,
			body:        "bad request detail",
			wantErr:     true,
			wantContain: "bad request detail",
		},
		{
			name:        "server error omits upstream body",
			statusCode:  http.StatusInternalServerError,
			body:        "database password leaked in stack trace",
			wantErr:     true,
			wantContain: "status 500",
			wantOmit:    "database password leaked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := servicesHTTPStatusError("executequery", tt.statusCode, []byte(tt.body))
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantContain != "" && !strings.Contains(err.Error(), tt.wantContain) {
				t.Fatalf("expected error to include %q, got %q", tt.wantContain, err.Error())
			}
			if tt.wantOmit != "" && strings.Contains(err.Error(), tt.wantOmit) {
				t.Fatalf("expected error to omit %q, got %q", tt.wantOmit, err.Error())
			}
		})
	}
}

var _ io.ReadCloser = (*trackingReadCloser)(nil)
