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

// IsRestrictedIP reports whether the given IP must never be the target of an
// outbound request originating from a runbook task. Covers loopback, RFC1918
// private ranges, link-local (incl. cloud metadata 169.254.169.254),
// multicast, unspecified, and interface-local multicast addresses.
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
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast()
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
