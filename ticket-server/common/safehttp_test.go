package common

import (
	"net"
	"testing"
)

func TestIsRestrictedIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata
		{"0.0.0.0", true},
		{"100.64.0.1", true}, // CGNAT
		{"224.0.0.1", true},
		{"255.255.255.255", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		// IPv4-mapped IPv6 literals must not bypass the IPv4 ranges.
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"8.8.8.8", false},
		{"2606:4700::1111", false},
		{"::ffff:8.8.8.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			if got := IsRestrictedIP(net.ParseIP(tt.ip)); got != tt.want {
				t.Errorf("IsRestrictedIP(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		if !IsRestrictedIP(nil) {
			t.Error("IsRestrictedIP(nil) = false, want true")
		}
	})
}

func TestValidateURL(t *testing.T) {
	rejected := []string{
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/",
		"http://127.0.0.1:8080/admin",
		"http://10.0.0.5/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]/",
		"http://[::ffff:169.254.169.254]/latest/meta-data/",
		"http://",
	}
	for _, raw := range rejected {
		t.Run("rejects "+raw, func(t *testing.T) {
			if err := ValidateURL(raw); err == nil {
				t.Errorf("ValidateURL(%q) = nil, want error", raw)
			}
		})
	}

	accepted := []string{
		"https://8.8.8.8/",
		"http://[2606:4700::1111]/",
	}
	for _, raw := range accepted {
		t.Run("accepts "+raw, func(t *testing.T) {
			if err := ValidateURL(raw); err != nil {
				t.Errorf("ValidateURL(%q) = %v, want nil", raw, err)
			}
		})
	}
}
