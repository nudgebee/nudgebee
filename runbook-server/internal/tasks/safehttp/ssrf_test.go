package safehttp

import (
	"net"
	"testing"
)

func TestIsRestrictedIP_NAT64(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"NAT64 metadata endpoint", "64:ff9b::a9fe:a9fe", true},
		{"NAT64 loopback", "64:ff9b::7f00:1", true},
		{"NAT64 RFC1918 10.x", "64:ff9b::a00:1", true},
		{"NAT64 public IPv4", "64:ff9b::808:808", false},
		{"local-use NAT64 prefix", "64:ff9b:1::a9fe:a9fe", true},
		{"public IPv6", "2607:f8b0:4004:800::200e", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			if got := IsRestrictedIP(ip); got != tt.want {
				t.Errorf("IsRestrictedIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsRestrictedIP_CGNAT(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"CGNAT start", "100.64.0.1", true},
		{"CGNAT mid", "100.100.100.100", true},
		{"CGNAT end", "100.127.255.254", true},
		{"just outside CGNAT", "100.128.0.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			if got := IsRestrictedIP(ip); got != tt.want {
				t.Errorf("IsRestrictedIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsRestrictedIP_StandardRanges(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"RFC1918 10.x", "10.0.0.1", true},
		{"RFC1918 172.16.x", "172.16.0.1", true},
		{"RFC1918 192.168.x", "192.168.1.1", true},
		{"link-local metadata", "169.254.169.254", true},
		{"multicast", "224.0.0.1", true},
		{"unspecified", "0.0.0.0", true},
		{"public IP", "8.8.8.8", false},
		{"public IP 2", "1.1.1.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			if got := IsRestrictedIP(ip); got != tt.want {
				t.Errorf("IsRestrictedIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
