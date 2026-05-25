package network

import (
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
		assert.NotEmpty(t, resultMap["raw"])
		assert.NotEmpty(t, resultMap["server"])

		raw := resultMap["raw"].(string)
		assert.Contains(t, raw, "Domain Name: GOOGLE.COM") // Typical IANA/Verisign output format is UPPERCASE
	})

	// Security: ensure SSRF-style inputs for the WHOIS server are rejected
	// before any outbound connection is attempted.
	t.Run("Rejects SSRF server targets", func(t *testing.T) {
		ssrfTargets := []string{
			"127.0.0.1",         // loopback
			"localhost",         // loopback hostname
			"169.254.169.254",   // cloud metadata service
			"10.0.0.1",          // RFC1918 private
			"192.168.1.1",       // RFC1918 private
			"172.16.0.1",        // RFC1918 private
			"0.0.0.0",           // unspecified
			"::1",               // IPv6 loopback
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

	t.Run("Rejects malformed domains", func(t *testing.T) {
		params := map[string]any{
			"domain": "example.com\r\nINJECT",
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err, "expected newline-injected domain to be rejected")
	})
}
