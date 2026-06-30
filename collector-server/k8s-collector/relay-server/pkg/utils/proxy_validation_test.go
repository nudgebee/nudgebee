package utils

import "testing"

func TestValidateProxyTargetURL(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		blockPrivate bool
		wantErr      bool
	}{
		{"valid public https", "https://api.example.com/v1/query", false, false},
		{"valid public http with port", "http://203.0.113.10:9000/path", false, false},
		{"private allowed by default", "http://10.1.2.3:8080/metrics", false, false},
		{"private blocked when configured", "http://10.1.2.3:8080/metrics", true, true},
		{"ULA blocked when configured", "http://[fd00:ec2::254]/latest/meta-data", true, true},

		{"loopback v4 always blocked", "http://127.0.0.1:8080/", false, true},
		{"loopback hostname always blocked", "http://localhost:8080/", false, true},
		{"dotted localhost blocked", "http://foo.localhost/", false, true},
		{"loopback v6 always blocked", "http://[::1]:9000/", false, true},
		{"link-local always blocked", "http://169.254.0.5/", false, true},
		{"cloud metadata IP always blocked", "http://169.254.169.254/latest/meta-data/", false, true},
		{"gcp metadata hostname blocked", "http://metadata.google.internal/computeMetadata/v1/", false, true},
		{"gcp metadata trailing dot blocked", "http://metadata.google.internal./computeMetadata/v1/", false, true},
		{"localhost trailing dot blocked", "http://localhost.:8080/", false, true},
		{"link-local v6 with zone id blocked", "http://[fe80::1%25lo0]:8080/", false, true},
		{"unspecified address blocked", "http://0.0.0.0:8080/", false, true},

		{"bad scheme file", "file:///etc/passwd", false, true},
		{"bad scheme gopher", "gopher://127.0.0.1:70/", false, true},
		{"empty host", "http://", false, true},
		{"garbage", "://not a url", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProxyTargetURL(tt.rawURL, tt.blockPrivate)
			if tt.wantErr && err == nil {
				t.Errorf("ValidateProxyTargetURL(%q, %v) = nil, want error", tt.rawURL, tt.blockPrivate)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateProxyTargetURL(%q, %v) = %v, want nil", tt.rawURL, tt.blockPrivate, err)
			}
		})
	}
}
