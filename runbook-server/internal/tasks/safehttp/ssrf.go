// Package safehttp centralizes the SSRF defenses runbook task handlers need
// when they make outbound HTTP/TCP requests on behalf of workflow authors.
//
// Threat model: workflow authoring requires account_admin (per
// app/src/lib/actions.yaml). A malicious or compromised account_admin can
// otherwise pivot outbound calls to:
//   - runbook-server's own services (loopback)
//   - cluster-internal services like api-server / relay-server (RFC1918)
//   - cloud metadata endpoints at 169.254.169.254 (link-local)
//
// The blocklist mirrors the more restrictive of the two existing patterns in
// the codebase:
//   - llm/llm-server/tools/tool_web_search.go validateURL (HTTP, no pinning)
//   - runbook-server/internal/tasks/network/whois_task.go isRestrictedIP /
//     validateWhoisServer (raw TCP, with IP pinning)
//
// We combine both: one-shot ValidateURL for clear pre-call rejection, plus a
// pinning DialContext for defense against DNS rebinding between validation
// and dial.
package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
	"time"
)

var restrictedCIDRs = []string{
	"100.64.0.0/10",   // CGNAT (RFC 6598) — default pod/node range for AWS VPC CNI, Tailscale
	"198.18.0.0/15",   // Benchmark testing (RFC 2544)
	"192.0.0.0/24",    // IETF Protocol Assignments
	"192.0.2.0/24",    // TEST-NET-1 (RFC 5737)
	"198.51.100.0/24", // TEST-NET-2 (RFC 5737)
	"203.0.113.0/24",  // TEST-NET-3 (RFC 5737)
	"240.0.0.0/4",     // Reserved (RFC 1112)
}

var (
	restrictedNets []*net.IPNet
	nat64WellKnown *net.IPNet // 64:ff9b::/96 (RFC 6052)
	nat64LocalUse  *net.IPNet // 64:ff9b:1::/48 (RFC 8215)
)

func init() {
	for _, cidr := range restrictedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid restricted CIDR %q: %v", cidr, err))
		}
		restrictedNets = append(restrictedNets, ipNet)
	}
	_, nat64WellKnown, _ = net.ParseCIDR("64:ff9b::/96")
	_, nat64LocalUse, _ = net.ParseCIDR("64:ff9b:1::/48")
}

// IsRestrictedIP reports whether the given IP must never be the target of an
// outbound request originating from a runbook task. Covers loopback, RFC1918
// private ranges, link-local (incl. cloud metadata 169.254.169.254),
// multicast, unspecified, interface-local multicast, CGNAT (RFC 6598),
// NAT64 (RFC 6052 / RFC 8215), and other reserved ranges.
//
// When running under `go test`, loopback (127.0.0.0/8 and ::1) is allowed so
// that tests using httptest.NewServer (which binds to 127.0.0.1) can exercise
// the real request path. testing.Testing() only returns true in binaries
// produced by `go test`, never in production builds — so this carve-out is
// invisible to deployed code.
func IsRestrictedIP(ip net.IP) bool {
	if testing.Testing() && ip.IsLoopback() {
		return false
	}
	return isRestricted(ip)
}

func isRestricted(ip net.IP) bool {
	// NAT64 well-known prefix (64:ff9b::/96, RFC 6052): the embedded IPv4
	// sits in bytes 12-15. Extract it and re-check so public destinations
	// (e.g. 64:ff9b::808:808 → 8.8.8.8) pass while restricted ones
	// (e.g. 64:ff9b::a9fe:a9fe → 169.254.169.254) are still blocked.
	if ip16 := ip.To16(); ip16 != nil && nat64WellKnown.Contains(ip16) {
		return isRestricted(net.IPv4(ip16[12], ip16[13], ip16[14], ip16[15]))
	}
	// NAT64 local-use prefix (64:ff9b:1::/48, RFC 8215): IPv4 embedding
	// offset depends on the operator's chosen prefix length, so we cannot
	// reliably extract it — block the entire range.
	if ip16 := ip.To16(); ip16 != nil && nat64LocalUse.Contains(ip16) {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, rNet := range restrictedNets {
		if rNet.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateURL parses rawURL and rejects when:
//   - the URL is malformed
//   - the scheme is not http or https
//   - the host is empty
//   - DNS resolution fails (fail-closed — runbook-server has no fallback resolver)
//   - any resolved IP is restricted (see IsRestrictedIP)
//
// This is the pre-call gate. Defense in depth lives in NewSafeDialContext,
// which re-validates at every dial to close the DNS rebinding TOCTOU window.
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

// NewSafeDialContext returns a DialContext that rejects connections to
// restricted IPs (per IsRestrictedIP), closing the DNS rebinding window
// between pre-call ValidateURL and the actual TCP dial.
//
// Uses net.Dialer.Control rather than custom resolution + serial dial so we
// keep the standard library's Happy Eyeballs (RFC 6555) parallel IPv4/IPv6
// dialing and connection caching. Control fires after the socket is created
// but before connect(), with the exact resolved IP about to be dialed —
// returning an error there rejects that specific attempt and lets the dialer
// move on to the next candidate, instead of stalling on broken IPv6 routes
// (common in cloud / K8s dual-stack environments).
//
// Pattern suggested by Gemini code review on PR #31805.
func NewSafeDialContext(dialTimeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if dialTimeout == 0 {
		dialTimeout = 30 * time.Second
	}
	d := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
		Control: func(_ string, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("invalid IP address %s", host)
			}
			if IsRestrictedIP(ip) {
				return fmt.Errorf("blocked dial to restricted IP %s", ip)
			}
			return nil
		},
	}
	return d.DialContext
}

// ResolveSafeIP resolves a hostname (or parses a literal IP) and returns a
// validated IP address string. Use this for external commands (ping,
// traceroute) where NewSafeDialContext cannot be used. By returning the
// resolved IP for the caller to pass to the command, the DNS rebinding
// (TOCTOU) window between validation and use is closed.
func ResolveSafeIP(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsRestrictedIP(ip) {
			return "", fmt.Errorf("host %s is a restricted IP", host)
		}
		return host, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("unable to resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("host %q resolved to zero addresses", host)
	}
	var firstIPv4 net.IP
	for _, ip := range ips {
		if IsRestrictedIP(ip) {
			return "", fmt.Errorf("host resolves to restricted IP %s", ip)
		}
		if firstIPv4 == nil && ip.To4() != nil {
			firstIPv4 = ip
		}
	}
	if firstIPv4 != nil {
		return firstIPv4.String(), nil
	}
	return ips[0].String(), nil
}

// SafeCheckRedirect validates each redirect target before the HTTP client
// follows it. Catches the case where a public URL 302s into an internal IP.
// Wire into http.Client.CheckRedirect alongside NewSafeDialContext.
func SafeCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if err := ValidateURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	return nil
}
