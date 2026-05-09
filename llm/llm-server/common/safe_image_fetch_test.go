package common

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsPrivateOrLoopbackIP_Table(t *testing.T) {
	cases := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.1.1", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd00:ec2::254", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		assert.Equal(t, tc.private, IsPrivateOrLoopbackIP(ip), "ip=%s", tc.ip)
	}
}

func TestIsBlockedImageHost_Cases(t *testing.T) {
	for _, h := range []string{"localhost", "LOCALHOST", "metadata.google.internal", "metadata.azure.com", "169.254.169.254", "100.100.100.200", "fd00:ec2::254"} {
		assert.True(t, IsBlockedImageHost(h), "host=%s should be blocked", h)
	}
	for _, h := range []string{"example.com", "1.1.1.1", "8.8.8.8"} {
		assert.False(t, IsBlockedImageHost(h), "host=%s should not be blocked", h)
	}
}

func TestValidateImageURLHost_Errors(t *testing.T) {
	cases := []struct {
		raw     string
		wantSub string
	}{
		{"ftp://example.com/img.png", "scheme"},
		{"http://localhost/img.png", "blocked"},
		{"http://10.0.0.1/img.png", "private"},
		{"http://[::1]/img.png", "private"},
		{"http:///img.png", "empty hostname"},
	}
	for _, tc := range cases {
		u, _ := url.Parse(tc.raw)
		err := ValidateImageURLHost(u)
		assert.Error(t, err, "url=%s", tc.raw)
		assert.Contains(t, err.Error(), tc.wantSub, "url=%s", tc.raw)
	}
}

func TestFetchImageSafely_RejectsLocalServer(t *testing.T) {
	// httptest spawns a server on 127.0.0.1, which our DialContext must refuse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := FetchImageSafely(ctx, srv.URL, SafeFetchOptions{
		MaxSizeBytes: 1024,
		Timeout:      2 * time.Second,
	})
	assert.Error(t, err, "fetch from 127.0.0.1 must be refused")
	if err != nil {
		assert.True(t,
			strings.Contains(err.Error(), "private") ||
				strings.Contains(err.Error(), "blocked"),
			"unexpected error: %s", err.Error())
	}
}

func TestFetchImageSafely_RejectsBadScheme(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, _, err := FetchImageSafely(ctx, "ftp://example.com/x.png", SafeFetchOptions{MaxSizeBytes: 1024})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "scheme")
}

func TestFetchImageSafely_RejectsLiteralPrivateIP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_, _, err := FetchImageSafely(ctx, "http://10.0.0.1/x.png", SafeFetchOptions{MaxSizeBytes: 1024})
	assert.Error(t, err)
}
