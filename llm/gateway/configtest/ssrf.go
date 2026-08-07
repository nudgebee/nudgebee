package configtest

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// validateEgressURL checks the SHAPE of a tenant-supplied custom endpoint URL: an https
// scheme and a non-empty host. It deliberately does NOT pre-resolve the IP — that would be
// a TOCTOU/DNS-rebinding hazard. The actual SSRF boundary is enforced at DIAL time by
// ssrfDialControl (below), which validates the concrete connect IP on every connection.
func validateEgressURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL (e.g. https://host/v1)")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("must use https")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

// ssrfSafeHTTPClient re-checks the resolved IP at connect time and refuses
// private/link-local/loopback targets, so a host that resolves to an internal IP at dial
// time is blocked even if it looked public earlier (DNS-rebinding-safe).
var ssrfSafeHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: 8 * time.Second, Control: ssrfDialControl}).DialContext,
	},
}

// ssrfDialControl rejects a connection whose resolved address is a blocked egress IP.
func ssrfDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedEgressIP(ip) {
		return fmt.Errorf("blocked egress to disallowed address %s", host)
	}
	return nil
}

// isBlockedEgressIP reports whether an IP is in a range the gateway must never dial for a
// tenant-supplied endpoint: loopback, RFC1918/ULA private, link-local (169.254.0.0/16 —
// includes the 169.254.169.254 cloud-metadata endpoint), and the unspecified address.
func isBlockedEgressIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}
