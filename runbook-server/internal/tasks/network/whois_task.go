package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"nudgebee/runbook/internal/tasks/types"
	"regexp"
	"strings"
	"time"
)

// validWhoisServer matches valid hostnames/IPs for WHOIS servers.
// Limits to alphanumerics, dots, colons (IPv6), and hyphens.
var validWhoisServer = regexp.MustCompile(`^[a-zA-Z0-9.:-]+$`)

// isRestrictedIP reports whether the given IP must never be the target of an
// outbound connection originating from a runbook task. It covers loopback,
// RFC1918 private ranges, link-local, multicast, unspecified, and
// interface-local multicast addresses — anything that is not a routable
// public address and could be abused for SSRF against internal services or
// cloud metadata endpoints.
func isRestrictedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast()
}

// validateWhoisServer ensures the given WHOIS server host is safe to connect
// to, and returns the specific IP address to dial. Returning the resolved IP
// (rather than the hostname) is critical: it pins the connection to the exact
// address that was validated, closing a DNS rebinding / TOCTOU window where
// an attacker-controlled DNS server could return a public IP for the
// validation lookup and then a restricted IP (e.g. 127.0.0.1 or
// 169.254.169.254) for the subsequent dial.
//
// The lookup honors the supplied context so a slow or unresponsive resolver
// cannot hang the task indefinitely.
func validateWhoisServer(ctx context.Context, server string) (string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", fmt.Errorf("whois server cannot be empty")
	}
	// Reject embedded port/URL components and illegal characters.
	if !validWhoisServer.MatchString(server) {
		return "", fmt.Errorf("invalid whois server: contains illegal characters")
	}

	// If the caller already gave us a literal IP, validate it directly — no
	// DNS resolution needed, and no rebinding window to worry about.
	if ip := net.ParseIP(server); ip != nil {
		if isRestrictedIP(ip) {
			return "", fmt.Errorf("whois server %q is a restricted IP", server)
		}
		return ip.String(), nil
	}

	// Resolve via a context-aware lookup and reject any non-routable result.
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", server)
	if err != nil {
		return "", fmt.Errorf("failed to resolve whois server %q: %w", server, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("whois server %q did not resolve to any IP", server)
	}
	for _, ip := range ips {
		if isRestrictedIP(ip) {
			return "", fmt.Errorf("whois server %q resolves to a restricted IP (%s)", server, ip.String())
		}
	}
	// Pin the connection to the first validated IP.
	return ips[0].String(), nil
}

// WhoisTask implements the Task interface for querying WHOIS data.
type WhoisTask struct{}

func (t *WhoisTask) GetName() string {
	return "network.whois"
}

func (t *WhoisTask) GetDescription() string {
	return "Look up domain registration and ownership details."
}

func (t *WhoisTask) GetDisplayName() string {
	return "Whois"
}

func (t *WhoisTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	domain, ok := params["domain"].(string)
	if !ok || domain == "" {
		return nil, fmt.Errorf("domain parameter is required")
	}

	// Simple WHOIS implementation: Connect to whois.iana.org to find the referral, then query the referral.
	// Or hardcode major TLDs. For a robust task, we might just query iana or a general server like 'whois.google.com'
	// or try to find the authoritative server.

	// Security: Validate the domain parameter to avoid sending newline-injected
	// or otherwise malformed payloads over the WHOIS protocol (port 43).
	if !validWhoisServer.MatchString(domain) || strings.HasPrefix(domain, "-") {
		return nil, fmt.Errorf("invalid domain format")
	}

	// Step 1: Query IANA or a high-level server.
	// Security: The `server` parameter is user-controlled and is used to open
	// an outbound TCP connection. Validate it to prevent SSRF attacks against
	// internal services, cloud metadata endpoints (169.254.169.254), etc.
	// We also pin the connection to the validated IP to close the DNS
	// rebinding (TOCTOU) window between validation and dial.
	server := "whois.iana.org"
	if s, ok := params["server"].(string); ok && s != "" {
		server = s
	}
	pinnedServer, err := validateWhoisServer(taskCtx.GetContext(), server)
	if err != nil {
		return nil, fmt.Errorf("invalid whois server: %w", err)
	}
	server = pinnedServer

	response, err := t.queryWhois(taskCtx, domain, server)
	if err != nil {
		return nil, err
	}

	// Step 2: Look for referral (refer: or whois:)
	// Security: The referral is parsed from an untrusted upstream response, so
	// it must be re-validated before we follow it — otherwise a malicious or
	// compromised upstream could redirect us to internal addresses. Pin the
	// referral to its resolved IP for the same reason as the initial server.
	referral := t.findReferral(response)
	if referral != "" && referral != server {
		pinnedReferral, err := validateWhoisServer(taskCtx.GetContext(), referral)
		if err != nil {
			taskCtx.GetLogger().Warn("Skipping whois referral to restricted server", "server", referral, "error", err.Error())
		} else {
			// Follow referral
			server = pinnedReferral
			response, err = t.queryWhois(taskCtx, domain, server)
			if err != nil {
				return nil, fmt.Errorf("referral query to %s failed: %w", server, err)
			}
		}
	}

	// Basic parsing (Expiration Date)
	// Key names vary wildly: "Registry Expiry Date", "Expiration Date", "paid-till", "expire", etc.
	// We will return the raw text and maybe attempt to extract a few common fields.

	return map[string]any{
		"domain": domain,
		"server": server,
		"raw":    response,
		"expiry": extractExpiry(response),
	}, nil
}

func (t *WhoisTask) queryWhois(taskCtx types.TaskContext, domain, server string) (string, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(taskCtx.GetContext(), "tcp", net.JoinHostPort(server, "43"))
	if err != nil {
		return "", err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			taskCtx.GetLogger().Warn("Failed to close WHOIS connection", "error", err)
		}
	}()

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		taskCtx.GetLogger().Warn("Failed to set WHOIS connection deadline", "error", err)
	}

	// Send query
	// Some servers require "domain <domain>" or just "<domain>"
	// Com and Net usually work with just domain.
	msg := fmt.Sprintf("%s\r\n", domain)
	_, err = conn.Write([]byte(msg))
	if err != nil {
		return "", err
	}

	// Read response
	result, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}

	return string(result), nil
}

func (t *WhoisTask) findReferral(raw string) string {
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "refer:") || strings.HasPrefix(lower, "whois:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func (t *WhoisTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"domain": {
				Type:        "string",
				Description: "Domain name to query.",
				Required:    true,
			},
			"server": {
				Type:        "string",
				Description: "Optional specific WHOIS server to query.",
				Required:    false,
			},
		},
	}
}

func (t *WhoisTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"raw": {
				Type:        "string",
				Description: "Raw WHOIS response text.",
				Required:    true,
			},
			"server": {
				Type:        "string",
				Description: "The authoritative WHOIS server that answered.",
				Required:    true,
			},
		},
	}
}
