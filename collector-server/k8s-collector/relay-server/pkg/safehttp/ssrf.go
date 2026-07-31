package safehttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

var restrictedCIDRs = []string{
	"100.64.0.0/10",   // CGNAT (RFC 6598)
	"198.18.0.0/15",   // Benchmark testing (RFC 2544)
	"192.0.0.0/24",    // IETF Protocol Assignments
	"192.0.2.0/24",    // TEST-NET-1 (RFC 5737)
	"198.51.100.0/24", // TEST-NET-2 (RFC 5737)
	"203.0.113.0/24",  // TEST-NET-3 (RFC 5737)
	"240.0.0.0/4",     // Reserved (RFC 1112)
	// NAT64 prefixes: an address here is translated by a NAT64 gateway to the
	// embedded IPv4 address, so 64:ff9b::a9fe:a9fe reaches 169.254.169.254.
	// To4() returns nil for these, so the IsPrivate/IsLinkLocal checks miss them.
	"64:ff9b::/96",   // Well-known NAT64 prefix (RFC 6052)
	"64:ff9b:1::/48", // Local-use NAT64 prefix (RFC 8215)
}

var restrictedNets []*net.IPNet

func init() {
	for _, cidr := range restrictedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid restricted CIDR %q: %v", cidr, err))
		}
		restrictedNets = append(restrictedNets, ipNet)
	}
}

func isRestrictedIP(ip net.IP) bool {
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

// ValidateURL rejects URLs targeting internal/private IPs, non-HTTP schemes,
// and unresolvable hosts. Call before making any outbound HTTP request with a
// user-supplied URL.
func ValidateURL(ctx context.Context, rawURL string) error {
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
		if isRestrictedIP(ip) {
			return fmt.Errorf("URL host %s is a restricted IP", host)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("unable to resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to zero addresses", host)
	}
	for _, ip := range ips {
		if isRestrictedIP(ip) {
			return fmt.Errorf("URL resolves to restricted IP %s", ip)
		}
	}
	return nil
}

// NewSafeDialContext returns a DialContext that rejects connections to
// restricted IPs at dial time, closing the DNS-rebinding window between
// ValidateURL and the actual TCP connect.
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
			if isRestrictedIP(ip) {
				return fmt.Errorf("blocked dial to restricted IP %s", ip)
			}
			return nil
		},
	}
	return d.DialContext
}

// NewSafeTransport clones http.DefaultTransport (preserving connection pooling,
// HTTP/2, proxy support, TLS timeouts) and overrides only DialContext with SSRF
// protection.
func NewSafeTransport(dialTimeout time.Duration) *http.Transport {
	var t *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		t = dt.Clone()
	} else {
		t = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	t.DialContext = NewSafeDialContext(dialTimeout)
	return t
}

// SafeCheckRedirect validates each redirect target before following it,
// preventing a public URL from redirecting into an internal IP.
func SafeCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if err := ValidateURL(req.Context(), req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	return nil
}
