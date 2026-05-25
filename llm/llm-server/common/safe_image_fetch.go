package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SafeFetchOptions configures a safe image fetch.
type SafeFetchOptions struct {
	// MaxSizeBytes caps the response body. Zero means unlimited.
	MaxSizeBytes int64
	// Timeout applies to the full request. Defaults to 10s when zero.
	Timeout time.Duration
	// AllowedMIMEPrefixes restricts accepted Content-Type. Defaults to {"image/"}.
	AllowedMIMEPrefixes []string
}

var (
	safeImageHTTPClientOnce sync.Once
	safeImageHTTPClient     *http.Client
)

// SafeImageHTTPClient returns a shared http.Client whose Transport refuses to
// dial any address whose resolved IP is private/loopback/link-local. This
// closes the DNS rebinding TOCTOU window between hostname validation and the
// actual fetch — every connection (including redirect hops) re-resolves and
// re-checks the destination IP. CheckRedirect additionally re-applies URL
// validation on every redirect target.
func SafeImageHTTPClient() *http.Client {
	safeImageHTTPClientOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		transport := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("safe-fetch: invalid addr %q: %w", addr, err)
				}
				if IsBlockedImageHost(host) {
					return nil, fmt.Errorf("safe-fetch: host %q is blocked", host)
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, fmt.Errorf("safe-fetch: dns lookup failed: %w", err)
				}
				for _, ipa := range ips {
					if IsPrivateOrLoopbackIP(ipa.IP) {
						return nil, fmt.Errorf("safe-fetch: refused dial to private ip %s", ipa.IP)
					}
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("safe-fetch: no addresses for host %q", host)
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
			MaxIdleConns:          50,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		safeImageHTTPClient = &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("safe-fetch: too many redirects")
				}
				if err := ValidateImageURLHost(req.URL); err != nil {
					return err
				}
				return nil
			},
		}
	})
	return safeImageHTTPClient
}

// FetchImageSafely retrieves an image from rawURL with SSRF guards. The
// returned data is bounded by opts.MaxSizeBytes; the Content-Type is checked
// against opts.AllowedMIMEPrefixes (defaults to "image/"). Hostname/IP
// validation runs both pre-fetch and at dial time so DNS rebinding cannot
// reach a private address.
func FetchImageSafely(ctx context.Context, rawURL string, opts SafeFetchOptions) (string, []byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, fmt.Errorf("safe-fetch: invalid url: %w", err)
	}
	if err := ValidateImageURLHost(parsed); err != nil {
		return "", nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", nil, fmt.Errorf("safe-fetch: build request: %w", err)
	}

	resp, err := SafeImageHTTPClient().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("safe-fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("safe-fetch: status %d", resp.StatusCode)
	}

	allowedPrefixes := opts.AllowedMIMEPrefixes
	if len(allowedPrefixes) == 0 {
		allowedPrefixes = []string{"image/"}
	}

	var body io.Reader = resp.Body
	if opts.MaxSizeBytes > 0 {
		body = io.LimitReader(resp.Body, opts.MaxSizeBytes+1)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", nil, fmt.Errorf("safe-fetch: read body: %w", err)
	}
	if opts.MaxSizeBytes > 0 && int64(len(data)) > opts.MaxSizeBytes {
		return "", nil, fmt.Errorf("safe-fetch: response exceeds %d bytes", opts.MaxSizeBytes)
	}

	mimeType := resp.Header.Get("Content-Type")
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	if mimeType == "" || !mimePrefixAllowed(mimeType, allowedPrefixes) {
		sniffed := http.DetectContentType(data)
		if idx := strings.Index(sniffed, ";"); idx != -1 {
			sniffed = strings.TrimSpace(sniffed[:idx])
		}
		mimeType = sniffed
	}
	if !mimePrefixAllowed(mimeType, allowedPrefixes) {
		return "", nil, fmt.Errorf("safe-fetch: disallowed content type %q", mimeType)
	}

	return mimeType, data, nil
}

func mimePrefixAllowed(mt string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(mt, p) {
			return true
		}
	}
	return false
}

// ValidateImageURLHost performs a hostname/scheme pre-check before fetch.
// It is a coarse pre-filter; the SafeImageHTTPClient's DialContext is the
// authoritative SSRF guard.
func ValidateImageURLHost(u *url.URL) error {
	if u == nil {
		return errors.New("safe-fetch: nil url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("safe-fetch: scheme %q not allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("safe-fetch: empty hostname")
	}
	if IsBlockedImageHost(host) {
		return fmt.Errorf("safe-fetch: host %q is blocked", host)
	}
	if ip := net.ParseIP(host); ip != nil && IsPrivateOrLoopbackIP(ip) {
		return fmt.Errorf("safe-fetch: literal private ip %s", host)
	}
	return nil
}

var blockedImageHostLiterals = map[string]struct{}{
	"localhost":                {},
	"ip6-localhost":            {},
	"ip6-loopback":             {},
	"metadata.google.internal": {},
	"metadata.azure.com":       {},
	"169.254.169.254":          {}, // AWS / Azure / GCP IMDS
	"100.100.100.200":          {}, // Alibaba metadata
	"fd00:ec2::254":            {}, // AWS IMDS over IPv6
}

// IsBlockedImageHost rejects hostnames known to map to cloud metadata
// services or local-only addresses. This is a coarse pre-filter; the
// authoritative SSRF guard is IP-based at dial time.
func IsBlockedImageHost(host string) bool {
	lower := strings.ToLower(strings.Trim(host, "[]"))
	if _, ok := blockedImageHostLiterals[lower]; ok {
		return true
	}
	return false
}

// IsPrivateOrLoopbackIP is the canonical IP check used at dial time. It
// rejects loopback, IPv4/IPv6 private ranges, link-local, multicast, and
// unspecified addresses, plus the IPv6 ULA range used by AWS for IMDS.
func IsPrivateOrLoopbackIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	// IPv6 unique-local (fc00::/7) — net.IP.IsPrivate covers fc00::/7 in Go 1.17+,
	// but some platforms don't include the AWS fd00:ec2::/64 IMDS range explicitly;
	// this is belt-and-suspenders.
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil {
		if v6[0] == 0xfc || v6[0] == 0xfd {
			return true
		}
	}
	return false
}
