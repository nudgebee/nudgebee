package spend

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestActiveAccountQueriesExcludeProxyAgents guards the two account-selection
// queries against silently dropping the non-proxy filter. A cloud account can
// carry both a real and a `proxy` agent row (UNIQUE on tenant, cloud_account_id,
// type); the authoritative opencostConnection lives only on the non-proxy row.
// Without `a.type != 'proxy'`, a connected proxy row (which has no
// opencostConnection key) would force-select the account for server-side sync
// even while the real agent runs its own in-cluster OpenCost — a double-write.
func TestActiveAccountQueriesExcludeProxyAgents(t *testing.T) {
	for name, q := range map[string]string{
		"activeK8sAccountsQuery":     activeK8sAccountsQuery,
		"activeK8sAccountsByIdQuery": activeK8sAccountsByIdQuery,
	} {
		if !strings.Contains(q, "a.type != 'proxy'") {
			t.Errorf("%s must filter out proxy agent rows (a.type != 'proxy')", name)
		}
		if !strings.Contains(q, "'opencostConnection') IS DISTINCT FROM 'true'") {
			t.Errorf("%s must guard on opencostConnection IS DISTINCT FROM 'true'", name)
		}
	}
}

func TestIsEmptyAllocation(t *testing.T) {
	empty := []string{"", "null", "[]", "{}", "  []  ", " null "}
	for _, s := range empty {
		if !isEmptyAllocation(json.RawMessage(s)) {
			t.Errorf("isEmptyAllocation(%q) = false, want true", s)
		}
	}
	nonEmpty := []string{`[{"node-1":{}}]`, `[{}]`, `{"a":1}`}
	for _, s := range nonEmpty {
		if isEmptyAllocation(json.RawMessage(s)) {
			t.Errorf("isEmptyAllocation(%q) = true, want false", s)
		}
	}
}

func TestParseWindow(t *testing.T) {
	cases := []struct {
		name       string
		window     string
		start, end int64
		ok         bool
	}{
		{"valid", "1704067200,1704153600", 1704067200, 1704153600, true},
		{"empty window means caught up", "0,0", 0, 0, false},
		{"zero start", "0,1704153600", 0, 0, false},
		{"zero end", "1704067200,0", 0, 0, false},
		{"end before start", "1704153600,1704067200", 0, 0, false},
		{"end equals start", "1704067200,1704067200", 0, 0, false},
		{"garbage", "not-a-window", 0, 0, false},
		{"empty string", "", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := parseWindow(tc.window)
			if ok != tc.ok || start != tc.start || end != tc.end {
				t.Fatalf("parseWindow(%q) = (%d, %d, %v), want (%d, %d, %v)",
					tc.window, start, end, ok, tc.start, tc.end, tc.ok)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate([]byte("short"), 10); got != "short" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncate([]byte("abcdefghij"), 3); got != "abc…" {
		t.Fatalf("truncate long = %q", got)
	}
}
