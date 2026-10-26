package common

import (
	"fmt"
	"net"
	"net/url"
)

var restrictedNets []*net.IPNet

func init() {
	cidrs := []string{
		"127.0.0.0/8",          // IPv4 loopback
		"::1/128",              // IPv6 loopback
		"10.0.0.0/8",           // RFC1918
		"172.16.0.0/12",        // RFC1918
		"192.168.0.0/16",       // RFC1918
		"169.254.0.0/16",       // IPv4 link-local / AWS & cloud metadata
		"fe80::/10",            // IPv6 link-local
		"fc00::/7",             // IPv6 unique local
		"0.0.0.0/8",            // RFC 1122 "this network"
		"100.64.0.0/10",        // CGNAT (RFC 6598)
		"192.0.0.0/24",         // IETF protocol assignments
		"192.0.2.0/24",         // TEST-NET-1
		"198.51.100.0/24",      // TEST-NET-2
		"203.0.113.0/24",       // TEST-NET-3
		"224.0.0.0/4",          // Multicast
		"240.0.0.0/4",          // Reserved
		"255.255.255.255/32",   // Limited broadcast
	}
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			restrictedNets = append(restrictedNets, ipNet)
		}
	}
}

// IsRestrictedIP checks if an IP belongs to private, loopback, or metadata ranges.
func IsRestrictedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalize IPv4-mapped IPv6 addresses (::ffff:127.0.0.1) to their 4-byte
	// form. The stdlib predicates and IPNet.Contains unwrap these today, but
	// normalizing here keeps the check correct independently of that detail.
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return true
	}
	for _, rNet := range restrictedNets {
		if rNet.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL rejects URLs targeting internal/private IPs, metadata service, or invalid schemes.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q: only http and https are allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host in URL")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsRestrictedIP(ip) {
			return fmt.Errorf("URL host %s is a restricted IP", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("unable to resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to zero addresses", host)
	}
	for _, ip := range ips {
		if IsRestrictedIP(ip) {
			return fmt.Errorf("URL resolves to restricted IP %s", ip)
		}
	}
	return nil
}
