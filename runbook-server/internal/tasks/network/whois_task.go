package network

import (
	"context"
	"fmt"
	"io"
	"net"
	"nudgebee/runbook/internal/tasks/safehttp"
	"nudgebee/runbook/internal/tasks/types"
	"regexp"
	"strings"
	"time"
)

// validWhoisServer matches valid hostnames/IPs for WHOIS servers.
// Limits to alphanumerics, dots, colons (IPv6), and hyphens.
var validWhoisServer = regexp.MustCompile(`^[a-zA-Z0-9.:-]+$`)

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
		if safehttp.IsRestrictedIP(ip) {
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
		if safehttp.IsRestrictedIP(ip) {
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
	//
	// serverHost and dialIP are deliberately kept apart: dialIP is the pinned
	// address we actually connect to, serverHost is the hostname we report, so
	// the output stays readable and stable across registry IP changes.
	serverHost := "whois.iana.org"
	if s, ok := params["server"].(string); ok && s != "" {
		serverHost = s
	}
	dialIP, err := validateWhoisServer(taskCtx.GetContext(), serverHost)
	if err != nil {
		return nil, fmt.Errorf("invalid whois server: %w", err)
	}

	response, err := t.queryWhois(taskCtx, domain, dialIP)
	if err != nil {
		// Soft-fail: a lookup that never completed is an outcome, not a task
		// error. Mirrors network.tcp, which reports unreachable hosts the same
		// way rather than failing the run.
		return whoisResult(domain, serverHost, "", false, whoisStatusLookupFailed,
			whoisRecord{}, fmt.Sprintf("whois query to %s failed: %v", serverHost, err)), nil
	}

	// Step 2: Look for referral (refer: or whois:)
	// Security: The referral is parsed from an untrusted upstream response, so
	// it must be re-validated before we follow it — otherwise a malicious or
	// compromised upstream could redirect us to internal addresses. Pin the
	// referral to its resolved IP for the same reason as the initial server.
	referral := t.findReferral(response)
	referralFollowed := false
	referralErr := ""
	// Scope the bootstrap handling to a response we actually got from IANA.
	// Registries in RPSL-ish formats can carry a "source: IANA" line of their
	// own; treating one of those as a bootstrap record would throw away a
	// perfectly good answer.
	fromIANA := strings.EqualFold(strings.TrimSpace(serverHost), "whois.iana.org") && isIANABootstrap(response)
	// Some registries (Nominet, for one) publish no WHOIS server to IANA, so
	// the bootstrap record comes back with an empty `refer:` / `whois:` field.
	// Without a fallback we would hand back IANA's record for the TLD itself
	// and report every .uk domain as registered. whois.nic.<tld> is the
	// convention those registries follow.
	if referral == "" && fromIANA {
		referral = nicServerForDomain(domain)
	}
	if referral != "" && referral != serverHost {
		pinnedReferral, err := validateWhoisServer(taskCtx.GetContext(), referral)
		if err != nil {
			taskCtx.GetLogger().Warn("Skipping whois referral to restricted server", "server", referral, "error", err.Error())
			referralErr = fmt.Sprintf("skipped referral to %s: %v", referral, err)
		} else {
			// Follow referral
			referred, err := t.queryWhois(taskCtx, domain, pinnedReferral)
			if err != nil {
				referralErr = fmt.Sprintf("referral query to %s failed: %v", referral, err)
			} else {
				serverHost = referral
				response = referred
				referralFollowed = true
			}
		}
	}

	rec := parseWhois(response)
	registered, status := classify(response, rec)
	// referralErr is only ever set when the referral was not followed, so this
	// surfaces a skipped or failed referral instead of burying it in the log.
	errMsg := referralErr

	// If we never followed the referral, `response` is the bootstrap server's
	// TLD record (which carries its own created/changed dates and the registry's
	// name servers) rather than a record for the domain. Reporting that as
	// "found" would be a lie.
	if !referralFollowed && fromIANA {
		registered = false
		rec = whoisRecord{}
		if referralErr != "" {
			status = whoisStatusLookupFailed
		} else {
			status = whoisStatusParseError
		}
	}

	return whoisResult(domain, serverHost, response, registered, status, rec, errMsg), nil
}

// whoisResult builds the task's structured output. Every key is always present
// so consumers can branch on `status` / `registered` without probing for
// missing fields.
func whoisResult(domain, server, raw string, registered bool, status string, rec whoisRecord, errMsg string) map[string]any {
	if rec.Nameservers == nil {
		rec.Nameservers = []string{}
	}
	if rec.DomainStatus == nil {
		rec.DomainStatus = []string{}
	}
	return map[string]any{
		"domain":             domain,
		"server":             server,
		"raw":                raw,
		"registered":         registered,
		"status":             status,
		"expiry":             rec.Expiry,
		"registrar":          rec.Registrar,
		"created":            rec.Created,
		"updated":            rec.Updated,
		"nameservers":        rec.Nameservers,
		"domain_status":      rec.DomainStatus,
		"registrant_country": rec.RegistrantCountry,
		"error":              errMsg,
	}
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

// ianaBootstrapPatterns identify a response that came from IANA's bootstrap
// server, which answers with a record for the TLD rather than for the domain.
var ianaBootstrapPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)^%\s*IANA WHOIS server`),
	regexp.MustCompile(`(?im)^source:\s*IANA\s*$`),
}

func isIANABootstrap(raw string) bool {
	for _, re := range ianaBootstrapPatterns {
		if re.MatchString(raw) {
			return true
		}
	}
	return false
}

// nicServerForDomain returns the conventional registry WHOIS host for a
// domain's TLD, or "" when the domain has no usable TLD. The result is still
// run through validateWhoisServer before anything is dialed.
func nicServerForDomain(domain string) string {
	// A fully qualified name may carry a trailing root dot.
	domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
	tld := domain[strings.LastIndex(domain, ".")+1:]
	if tld == "" || tld == domain {
		return ""
	}
	return "whois.nic." + strings.ToLower(tld)
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
			"domain": {
				Type:        "string",
				Description: "The domain that was queried.",
				Required:    true,
			},
			"registered": {
				Type:        "boolean",
				Description: "True when the registry returned a record for the domain.",
				Required:    true,
			},
			"status": {
				Type:        "string",
				Description: "Outcome of the lookup: found, not_found, rate_limited, parse_error or lookup_failed.",
				Required:    true,
			},
			"raw": {
				Type:        "string",
				Description: "Raw WHOIS response text.",
				Required:    true,
			},
			"server": {
				Type:        "string",
				Description: "Hostname of the authoritative WHOIS server that answered.",
				Required:    true,
			},
			"expiry": {
				Type:        "string",
				Description: "Domain expiration date (RFC3339), null when not present or unparseable.",
				Required:    false,
			},
			"registrar": {
				Type:        "string",
				Description: "Sponsoring registrar.",
				Required:    false,
			},
			"created": {
				Type:        "string",
				Description: "Domain creation date (RFC3339 when parseable, otherwise as reported).",
				Required:    false,
			},
			"updated": {
				Type:        "string",
				Description: "Date the record was last updated (RFC3339 when parseable, otherwise as reported).",
				Required:    false,
			},
			"nameservers": {
				Type:        "array",
				Description: "Authoritative name servers, lowercased and deduplicated.",
				Required:    false,
			},
			"domain_status": {
				Type:        "array",
				Description: "EPP/registry status codes for the domain.",
				Required:    false,
			},
			"registrant_country": {
				Type:        "string",
				Description: "Registrant country as reported by the registry.",
				Required:    false,
			},
			"error": {
				Type:        "string",
				Description: "Error message when the lookup could not be completed.",
				Required:    false,
			},
		},
	}
}
