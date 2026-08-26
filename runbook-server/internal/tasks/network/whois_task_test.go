package network

import (
	"net"
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWhoisTask_Execute(t *testing.T) {
	task := &WhoisTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	t.Run("Whois google.com", func(t *testing.T) {
		// Requires network access to port 43
		params := map[string]any{
			"domain": "google.com",
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			t.Logf("Whois failed (network?): %v", err)
			return
		}

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		if resultMap["status"] == whoisStatusLookupFailed {
			t.Logf("Whois lookup failed (network?): %v", resultMap["error"])
			return
		}

		assert.NotEmpty(t, resultMap["raw"])
		assert.NotEmpty(t, resultMap["server"])

		// The reported server must be the registry hostname, not the pinned IP
		// we dialed — the hostname is readable and survives registry IP changes.
		server := resultMap["server"].(string)
		assert.Nil(t, net.ParseIP(server), "server %q should be a hostname, not an IP", server)

		assert.Equal(t, true, resultMap["registered"])
		assert.Equal(t, whoisStatusFound, resultMap["status"])
		assert.NotEmpty(t, resultMap["registrar"])
		assert.NotEmpty(t, resultMap["created"])
		assert.NotEmpty(t, resultMap["updated"])
		assert.NotEmpty(t, resultMap["nameservers"])

		raw := resultMap["raw"].(string)
		assert.Contains(t, raw, "Domain Name: GOOGLE.COM") // Typical IANA/Verisign output format is UPPERCASE
	})

	t.Run("Reports an unregistered domain as not_found", func(t *testing.T) {
		params := map[string]any{
			"domain": "asdkjhasd-nudgebee-does-not-exist.com",
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			t.Logf("Whois failed (network?): %v", err)
			return
		}

		resultMap := res.(map[string]any)
		if resultMap["status"] == whoisStatusLookupFailed {
			t.Logf("Whois lookup failed (network?): %v", resultMap["error"])
			return
		}

		assert.Equal(t, false, resultMap["registered"])
		assert.Equal(t, whoisStatusNotFound, resultMap["status"])
	})

	// Security: ensure SSRF-style inputs for the WHOIS server are rejected
	// before any outbound connection is attempted.
	t.Run("Rejects SSRF server targets", func(t *testing.T) {
		ssrfTargets := []string{
			"169.254.169.254",   // cloud metadata service
			"10.0.0.1",          // RFC1918 private
			"192.168.1.1",       // RFC1918 private
			"172.16.0.1",        // RFC1918 private
			"0.0.0.0",           // unspecified
			"fe80::1",           // IPv6 link-local
			"whois.iana.org\n-", // illegal characters
			"whois.iana.org|ls", // shell-style injection
		}
		for _, target := range ssrfTargets {
			params := map[string]any{
				"domain": "example.com",
				"server": target,
			}
			_, err := task.Execute(taskCtx, params)
			assert.Error(t, err, "expected %q to be rejected as an unsafe WHOIS server", target)
		}
	})

	// Loopback (127.0.0.1, localhost, ::1) is blocked in production but
	// deliberately allowed inside a `go test` binary — see the
	// testing.Testing() carve-out in safehttp.IsRestrictedIP — so it cannot be
	// asserted as rejected here. It exercises the soft-fail path instead:
	// nothing listens on port 43 locally, so the lookup fails without failing
	// the task.
	t.Run("Reports an unreachable server as lookup_failed", func(t *testing.T) {
		params := map[string]any{
			"domain": "example.com",
			"server": "127.0.0.1",
		}

		res, err := task.Execute(taskCtx, params)
		assert.NoError(t, err, "an unreachable server is an outcome, not a task error")

		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "127.0.0.1", resultMap["server"])
		assert.Equal(t, false, resultMap["registered"])
		assert.Equal(t, whoisStatusLookupFailed, resultMap["status"])
		assert.NotEmpty(t, resultMap["error"])
	})

	t.Run("Rejects malformed domains", func(t *testing.T) {
		params := map[string]any{
			"domain": "example.com\r\nINJECT",
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err, "expected newline-injected domain to be rejected")
	})
}

// IANA answers a query for an unknown-to-it domain with the record for the
// TLD. For .uk that record has empty refer:/whois: fields, so there is no
// referral to follow — and it parses as a perfectly good "registered domain"
// unless we recognise where it came from.
const rawIANABootstrapUK = `% IANA WHOIS server
% This query returned 1 object

refer:

domain:       UK

organisation: Nominet UK

nserver:      DNS1.NIC.UK 213.248.216.1
nserver:      DNS2.NIC.UK 103.49.80.1

whois:

status:       ACTIVE
created:      1985-07-24
changed:      2026-08-04
source:       IANA
`

func TestIsIANABootstrap(t *testing.T) {
	task := &WhoisTask{}

	assert.True(t, isIANABootstrap(rawIANABootstrapUK))
	assert.Empty(t, task.findReferral(rawIANABootstrapUK), "the .uk bootstrap record carries no referral")

	// Without the bootstrap guard this record would be reported as a
	// registered domain with the registry's own name servers.
	registered, status := classify(rawIANABootstrapUK, parseWhois(rawIANABootstrapUK))
	assert.True(t, registered)
	assert.Equal(t, whoisStatusFound, status)

	assert.False(t, isIANABootstrap(rawComRegistered))
	assert.False(t, isIANABootstrap(rawComNotFound))
}

func TestNicServerForDomain(t *testing.T) {
	tests := map[string]string{
		"asdkjhasd.co.uk": "whois.nic.uk",
		"google.CO.UK":    "whois.nic.uk",
		"example.io":      "whois.nic.io",
		"google.co.uk.":   "whois.nic.uk", // trailing root dot
		" example.io ":    "whois.nic.io",
		"nodot":           "",
	}
	for domain, want := range tests {
		assert.Equal(t, want, nicServerForDomain(domain), "domain %q", domain)
	}
}
