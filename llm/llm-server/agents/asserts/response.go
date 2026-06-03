//go:build e2e

package asserts

import (
	"strings"
	"testing"

	"nudgebee/llm/agents/core"

	"github.com/stretchr/testify/assert"
)

// ============================================================
// Tier 2 — response content
// ============================================================

// ResponseStatus asserts the conversation reached the expected final
// status. Defaults are not applied here — pass the explicit status you
// want.
func ResponseStatus(t *testing.T, resp core.NBAgentResponse, want core.ConversationStatus) bool {
	t.Helper()
	return assert.Equal(t, want, resp.Status,
		"unexpected final status (response=%q)",
		truncate(joinResponse(resp), 200))
}

// ResponseContains asserts the joined response body contains the given
// substring, case-insensitively.
func ResponseContains(t *testing.T, resp core.NBAgentResponse, want string) bool {
	t.Helper()
	body := strings.ToLower(joinResponse(resp))
	if strings.Contains(body, strings.ToLower(want)) {
		return true
	}
	t.Errorf("expected response to contain %q; got %q",
		want, truncate(joinResponse(resp), 400))
	return false
}

// ResponseContainsAny asserts at least one of the substrings appears in
// the response body, case-insensitively. Use for "the answer should
// mention one of these concepts" checks.
func ResponseContainsAny(t *testing.T, resp core.NBAgentResponse, anyOf []string) bool {
	t.Helper()
	if len(anyOf) == 0 {
		return true
	}
	body := strings.ToLower(joinResponse(resp))
	for _, kw := range anyOf {
		if strings.Contains(body, strings.ToLower(kw)) {
			return true
		}
	}
	t.Errorf("expected response to contain one of %v; got %q",
		anyOf, truncate(joinResponse(resp), 400))
	return false
}

// ResponseContainsAll asserts every substring appears in the response
// body, case-insensitively. Stricter than ContainsAny.
func ResponseContainsAll(t *testing.T, resp core.NBAgentResponse, allOf []string) bool {
	t.Helper()
	if len(allOf) == 0 {
		return true
	}
	body := strings.ToLower(joinResponse(resp))
	ok := true
	for _, kw := range allOf {
		if !strings.Contains(body, strings.ToLower(kw)) {
			t.Errorf("expected response to contain %q (missing); body=%q",
				kw, truncate(joinResponse(resp), 400))
			ok = false
		}
	}
	return ok
}

// ResponseNotContains asserts none of the substrings appear in the
// response body, case-insensitively. Use for forbidden phrases like
// "connection refused", "unable to investigate", "permission denied" —
// these often indicate the agent gave up rather than investigated.
func ResponseNotContains(t *testing.T, resp core.NBAgentResponse, forbidden []string) bool {
	t.Helper()
	if len(forbidden) == 0 {
		return true
	}
	body := strings.ToLower(joinResponse(resp))
	ok := true
	for _, kw := range forbidden {
		if strings.Contains(body, strings.ToLower(kw)) {
			t.Errorf("response contains forbidden phrase %q; body=%q",
				kw, truncate(joinResponse(resp), 400))
			ok = false
		}
	}
	return ok
}

// ResponseLength asserts the response body length (in bytes) falls within
// [minBytes, maxBytes]. Pass 0 to disable an end. Useful as a sanity
// bound: too-short usually means agent gave up, too-long usually means
// no summarization.
func ResponseLength(t *testing.T, resp core.NBAgentResponse, minBytes, maxBytes int) bool {
	t.Helper()
	n := len(joinResponse(resp))
	ok := true
	if minBytes > 0 && n < minBytes {
		t.Errorf("response too short: got %d bytes, want >= %d", n, minBytes)
		ok = false
	}
	if maxBytes > 0 && n > maxBytes {
		t.Errorf("response too long: got %d bytes, want <= %d", n, maxBytes)
		ok = false
	}
	return ok
}

// truncate trims s to max bytes for inclusion in error messages so test
// logs stay readable when a 50KB response fails an assertion.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
