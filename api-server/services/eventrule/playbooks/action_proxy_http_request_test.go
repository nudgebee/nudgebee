package playbooks

import (
	"strings"
	"testing"
)

func TestValidateProxyURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// Blocked: loopback — would hit api-server's own services
		{"reject loopback IPv4", "http://127.0.0.1/admin", true, "restricted IP"},
		{"reject loopback localhost", "http://localhost/admin", true, "restricted IP"},
		{"reject IPv6 loopback", "http://[::1]/admin", true, "restricted IP"},

		// Blocked: link-local — cloud metadata endpoint (IMDS)
		{"reject cloud metadata", "http://169.254.169.254/latest/meta-data/", true, "restricted IP"},

		// Blocked: unspecified
		{"reject unspecified 0.0.0.0", "http://0.0.0.0/", true, "restricted IP"},

		// Blocked: bad schemes
		{"reject ftp scheme", "ftp://files.example.com/data", true, "unsupported scheme"},
		{"reject file scheme", "file:///etc/passwd", true, "unsupported scheme"},
		{"reject gopher scheme", "gopher://evil.com", true, "unsupported scheme"},
		{"reject no scheme", "example.com/path", true, "unsupported scheme"},

		// Blocked: missing host
		{"reject empty host", "http:///path", true, "missing host"},

		// Allowed: private RFC1918 — legitimate relay targets inside customer clusters
		{"allow private 10.x", "http://10.0.0.1/internal", false, ""},
		{"allow private 172.16.x", "http://172.16.0.1/internal", false, ""},
		{"allow private 192.168.x", "http://192.168.1.1/internal", false, ""},

		// Allowed: DNS failures pass through — relay resolves cluster-internal names
		{"allow unresolvable host", "http://prometheus.monitoring.svc.cluster.local/api/v1/query", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProxyURL(tt.url)
			if !tt.wantErr {
				if err != nil {
					t.Errorf("validateProxyURL(%q) unexpected error = %v", tt.url, err)
				}
				return
			}
			if err == nil {
				t.Errorf("validateProxyURL(%q) expected error containing %q, got nil", tt.url, tt.errMsg)
				return
			}
			if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validateProxyURL(%q) error = %q, want containing %q", tt.url, err.Error(), tt.errMsg)
			}
		})
	}
}
