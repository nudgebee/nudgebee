package safehttp

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRestrictedIP(t *testing.T) {
	tests := []struct {
		ip         string
		restricted bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"100.64.0.1", true},      // CGNAT
		{"100.127.255.254", true}, // CGNAT upper bound
		{"198.18.0.1", true},      // Benchmark testing
		{"240.0.0.1", true},       // Reserved
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"93.184.216.34", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip, "failed to parse IP %s", tt.ip)
			assert.Equal(t, tt.restricted, isRestrictedIP(ip))
		})
	}
}

func TestValidateURL(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Public cases use IP literals so the suite never depends on external DNS.
		{"valid https", "https://93.184.216.34/mcp", false},
		{"valid http", "http://8.8.8.8/mcp", false},
		{"loopback IP", "http://127.0.0.1:8080/mcp", true},
		{"private IP", "http://10.0.0.1/mcp", true},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"CGNAT", "http://100.100.100.100/internal", true},
		{"ipv6 loopback", "http://[::1]:8080/mcp", true},
		{"ipv4-mapped metadata", "http://[::ffff:169.254.169.254]/", true},
		{"NAT64 metadata", "http://[64:ff9b::a9fe:a9fe]/", true},
		{"NAT64 private", "http://[64:ff9b::a00:1]/", true},
		{"ftp scheme", "ftp://93.184.216.34/file", true},
		{"empty host", "http:///path", true},
		{"no scheme", "93.184.216.34/mcp", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(ctx, tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSafeCheckRedirect_TooManyRedirects(t *testing.T) {
	via := make([]*http.Request, 10)
	req, _ := http.NewRequest("GET", "https://93.184.216.34", nil)
	err := SafeCheckRedirect(req, via)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopped after 10 redirects")
}

func TestSafeCheckRedirect_BlocksPrivateIP(t *testing.T) {
	via := make([]*http.Request, 1)
	req, _ := http.NewRequest("GET", "http://169.254.169.254/latest/meta-data/", nil)
	err := SafeCheckRedirect(req, via)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redirect blocked")
}

func TestSafeCheckRedirect_AllowsPublicURL(t *testing.T) {
	via := make([]*http.Request, 1)
	req, _ := http.NewRequest("GET", "https://93.184.216.34/callback", nil)
	err := SafeCheckRedirect(req, via)
	assert.NoError(t, err)
}

// stubRoundTripper is deliberately not an *http.Transport, so installing it as
// http.DefaultTransport forces NewSafeTransport down its fallback branch.
type stubRoundTripper struct{}

func (stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

func TestNewSafeTransport(t *testing.T) {
	tr := NewSafeTransport(30 * time.Second)
	assert.NotNil(t, tr)
	assert.NotNil(t, tr.DialContext)
	// Comma-ok rather than a bare assertion: anything in this binary may have
	// replaced DefaultTransport, and a panic here would mask the real result.
	if defaultTr, ok := http.DefaultTransport.(*http.Transport); ok {
		assert.Equal(t, defaultTr.MaxIdleConns, tr.MaxIdleConns)
		assert.Equal(t, defaultTr.IdleConnTimeout, tr.IdleConnTimeout)
	}
}

// TestNewSafeTransport_DefaultWrapped covers the branch taken when an APM or
// tracing library has wrapped http.DefaultTransport in a custom RoundTripper.
// Without this, the fallback constants are never executed by any test.
func TestNewSafeTransport_DefaultWrapped(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = stubRoundTripper{}
	defer func() { http.DefaultTransport = orig }()

	tr := NewSafeTransport(30 * time.Second)
	assert.NotNil(t, tr)
	assert.NotNil(t, tr.DialContext, "fallback transport must still be SSRF-safe")
	assert.Equal(t, 100, tr.MaxIdleConns)
	assert.Equal(t, 90*time.Second, tr.IdleConnTimeout)
	assert.True(t, tr.ForceAttemptHTTP2)
}
