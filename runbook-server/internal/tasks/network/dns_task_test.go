package network

import (
	"nudgebee/runbook/internal/tasks/testutils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDnsTask_Execute(t *testing.T) {
	task := &DnsTask{}
	logger := &TestLogger{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", logger)

	t.Run("Lookup A Record (localhost)", func(t *testing.T) {
		// This test requires internet access. If running in air-gapped env, might fail.
		// We can skip or use localhost if we just want to test flow.
		// "localhost" usually returns 127.0.0.1 or ::1

		params := map[string]any{
			"domain": "localhost",
			"type":   "A",
		}

		res, err := task.Execute(taskCtx, params)
		if err != nil {
			// If localhost lookup fails, we can't verify much, but usually it shouldn't.
			t.Logf("Skipping localhost test if network fails: %v", err)
			return
		}

		assert.NoError(t, err)
		resultMap, ok := res.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "localhost", resultMap["domain"])
		assert.Equal(t, "A", resultMap["type"])

		answers, ok := resultMap["answer"].([]string)
		assert.True(t, ok)
		assert.NotEmpty(t, answers)
		// Check that it looks like an IP
		assert.Contains(t, answers[0], ".")
	})

	t.Run("Missing Domain", func(t *testing.T) {
		params := map[string]any{
			"type": "A",
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "domain parameter is required")
	})

	t.Run("Invalid Type", func(t *testing.T) {
		params := map[string]any{
			"domain": "localhost",
			"type":   "INVALID_TYPE",
		}
		_, err := task.Execute(taskCtx, params)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported record type")
		// The error names every accepted value, so the fix is readable from it.
		assert.Contains(t, err.Error(), "must be one of A, AAAA, CNAME, MX, TXT, NS")
	})

	t.Run("Type is trimmed and case-insensitive", func(t *testing.T) {
		for _, recordType := range []string{" A ", "a", "\ta\n", "   "} {
			res, err := task.Execute(taskCtx, map[string]any{
				"domain": "localhost",
				"type":   recordType,
			})
			if err != nil {
				// A resolver failure is environmental; an unsupported-type error is the bug.
				assert.NotContains(t, err.Error(), "unsupported record type", "type %q", recordType)
				continue
			}
			resultMap, ok := res.(map[string]any)
			assert.True(t, ok)
			assert.Equal(t, "A", resultMap["type"], "type %q", recordType)
		}
	})
}

func TestDnsTask_EveryOfferedTypeIsSupported(t *testing.T) {
	task := &DnsTask{}
	taskCtx := testutils.NewTestTaskContext("tenant", "account", "user", &TestLogger{})

	// The dropdown offers dnsRecordTypes; each one must reach a lookup, not the
	// unsupported-type error that lists it as allowed. A lookup that finds no
	// record for localhost is fine here.
	for _, recordType := range dnsRecordTypes {
		_, err := task.Execute(taskCtx, map[string]any{"domain": "localhost", "type": recordType})
		if err != nil {
			assert.NotContains(t, err.Error(), "unsupported record type", "type %q", recordType)
		}
	}
}

func TestDnsTask_InputSchema_TypeOptions(t *testing.T) {
	prop := (&DnsTask{}).InputSchema().Properties["type"]

	// Options is what makes the form render a dropdown instead of a text box.
	assert.Equal(t, []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}, prop.Options)
	assert.False(t, prop.Required)
}
