package proxy

import (
	"strings"
	"testing"
)

// TestColdStartHint: 502/503/504 get the "starting up" hint (serverless / scale-to-zero
// warm-up); other statuses pass through unchanged. 503 also gets a distinct error code.
func TestColdStartHint(t *testing.T) {
	for _, s := range []int{502, 503, 504} {
		if got := coldStartHint(s, "unavailable"); !strings.Contains(got, "starting up") {
			t.Errorf("status %d: expected cold-start hint, got %q", s, got)
		}
	}
	if got := coldStartHint(401, "bad key"); got != "bad key" {
		t.Errorf("401 should pass through unchanged, got %q", got)
	}
	if got := upstreamErrorCode(503); got != "upstream_unavailable" {
		t.Errorf("503 code = %q, want upstream_unavailable", got)
	}
	if got := upstreamErrorCode(500); got != "upstream_error" {
		t.Errorf("500 code = %q, want upstream_error", got)
	}
}
