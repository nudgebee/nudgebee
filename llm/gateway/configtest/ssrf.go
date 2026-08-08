package configtest

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"nudgebee/llm-gateway/config"
)

// validateEgressURL checks the SHAPE of a tenant-supplied custom endpoint URL: an http/https
// scheme and a non-empty host (http is allowed for in-cluster endpoints). It deliberately
// does NOT pre-resolve the IP — that would be a TOCTOU/DNS-rebinding hazard. The actual SSRF
// boundary is enforced at DIAL time by ssrfDialControl (below), which validates the concrete
// connect IP on every connection (private IPs gated by the private-endpoints opt-in).
func validateEgressURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be a valid URL (e.g. https://host/v1)")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("must use http or https")
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
	if ip := net.ParseIP(host); ip != nil && isBlockedEgressIP(ip, config.Config.AllowPrivateEndpoints) {
		return fmt.Errorf("blocked egress to disallowed address %s", host)
	}
	return nil
}

// isBlockedEgressIP reports whether an IP is in a range the gateway must never dial for a
// tenant-supplied endpoint. Link-local (169.254.0.0/16 — includes the 169.254.169.254
// cloud-metadata endpoint) and the unspecified address are ALWAYS blocked. Loopback +
// RFC1918/ULA private ranges are blocked UNLESS the operator opted in via
// gateway_allow_private_endpoints (a gateway inside a private cluster reaching its own
// internal model servers). Mirrors bifrost's request-time dialer, so Test Connection
// agrees with the real request path.
func isBlockedEgressIP(ip net.IP, allowPrivate bool) bool {
	if ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if allowPrivate {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}
