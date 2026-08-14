package alertrule

import (
	"log/slog"
	"os"
	"testing"

	"nudgebee/services/security"
)

// TestLiveDatadogListAlertRules calls the real Datadog API using the credentials
// stored on an existing integration. It is the only check that exercises paging,
// auth, and response parsing against the actual service — everything else in
// this package is tested against a hand-built payload.
//
// Skipped unless explicitly enabled, so it never runs in CI:
//
//	LIVE_DATADOG_TENANT_ID=<tenant-uuid> \
//	LIVE_DATADOG_ACCOUNT_ID=<cloud-account-uuid> \
//	go test ./observability/alertrule/ -run TestLiveDatadogListAlertRules -v
//
// The process needs the same environment the api-server runs with (database
// connection + the key used to decrypt integration_config_values), because the
// credentials are read from the integration rather than passed in.
//
// Read-only: issues GET /api/v1/monitor and writes nothing, to Datadog or to
// the database.
func TestLiveDatadogListAlertRules(t *testing.T) {
	tenantId := os.Getenv("LIVE_DATADOG_TENANT_ID")
	accountId := os.Getenv("LIVE_DATADOG_ACCOUNT_ID")
	if tenantId == "" || accountId == "" {
		t.Skip("set LIVE_DATADOG_TENANT_ID and LIVE_DATADOG_ACCOUNT_ID to run this against a real Datadog account")
	}

	sc := security.NewRequestContextForTenantAdmin(tenantId, slog.Default(), nil, nil)

	source := &DatadogAlertRuleSource{}
	rules, err := source.ListAlertRules(sc, accountId)
	if err != nil {
		t.Fatalf("ListAlertRules failed: %v", err)
	}

	t.Logf("fetched %d Datadog monitors", len(rules))

	allowedSeverity := map[string]bool{"critical": true, "warning": true}
	allowedAlertType := map[string]bool{"log": true, "metric": true}

	for i, r := range rules {
		// Every mapped rule must satisfy the event_rules foreign keys, or the
		// sync will fail at write time with 23503 rather than here.
		if !allowedSeverity[r.Severity] {
			t.Errorf("monitor %q mapped to severity %q, which event_rule_severity does not contain", r.Name, r.Severity)
		}
		if !allowedAlertType[r.AlertType] {
			t.Errorf("monitor %q mapped to alert_type %q, which event_rule_alert_type does not contain", r.Name, r.AlertType)
		}
		if r.ExternalRuleId == "" {
			t.Errorf("monitor %q has no external_rule_id; update/delete could not target it later", r.Name)
		}
		// A rule with no name cannot be keyed into event_rules and is skipped by
		// the sync — worth surfacing rather than silently dropping.
		if r.Name == "" {
			t.Errorf("monitor at index %d has an empty name and would be skipped by the sync", i)
		}
		if i < 5 {
			t.Logf("  [%s] %-50s type=%-6s sev=%-8s enabled=%v", r.ExternalRuleId, r.Name, r.AlertType, r.Severity, r.Enabled)
		}
	}

	// Duplicate names collapse onto one event_rules row under the
	// (account, tenant, source, alert) key — report it here so the blast radius
	// is known before the first sync rather than discovered afterwards.
	byName := map[string]int{}
	for _, r := range rules {
		byName[r.Name]++
	}
	for name, n := range byName {
		if n > 1 {
			t.Logf("WARNING: %d monitors share the name %q — they will collapse into one event_rules row", n, name)
		}
	}
}
