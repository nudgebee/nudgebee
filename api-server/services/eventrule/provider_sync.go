package eventrule

import (
	"errors"
	"fmt"

	"nudgebee/services/internal/database"
	"nudgebee/services/observability/alertrule"
	"nudgebee/services/security"

	"github.com/lib/pq"
)

// ProviderSyncResult reports what one provider sync did. Counts are per
// account+provider so a caller can tell an empty provider ("nothing
// configured") from a failed one.
type ProviderSyncResult struct {
	Provider  string `json:"provider"`
	AccountId string `json:"account_id"`
	Fetched   int    `json:"fetched"`
	Upserted  int    `json:"upserted"`
	Skipped   int    `json:"skipped"`
	Disabled  int    `json:"disabled"`
	Error     string `json:"error,omitempty"`
}

// syncableProviders maps the source name accepted by the API onto the
// (provider, providerSource) pair the alertrule registry resolves on, plus the
// event_rules.source value written for synced rules.
type syncableProvider struct {
	provider       string
	providerSource string
	// ruleSource is written to event_rules.source. It is the provider's
	// *_webhook source so a synced definition lands on the same row as the
	// webhook that reports its firings, keeping one row per rule in the UI.
	//
	// Consequence, deliberate: upsertEventRule guards `expr`, `duration` and
	// `alert_type` behind `EXCLUDED.source NOT LIKE '%\_webhook'`, so on a row
	// that already exists those three fields are NOT refreshed by a sync —
	// only external_rule_id / provider_config / metric_provider are (their
	// guards have an extra OR for a webhook-sourced existing row). A rule the
	// sync creates first gets everything; a rule the webhook created first
	// keeps the webhook's expr. Relaxing those guards would reopen #33024,
	// where firing webhooks blanked agent-synced PromQL, because source is the
	// only signal separating a firing from a definition.
	ruleSource string
	// reconcileDeletions says whether "absent from the listing" can be trusted
	// to mean "deleted upstream". False where a *successful* listing may still
	// be partial for reasons the API does not report.
	reconcileDeletions bool
}

var syncableProviders = map[string]syncableProvider{
	"datadog":   {"datadog", "user", "datadog_webhook", true},
	"grafana":   {"grafana", "user", "grafana_webhook", true},
	"newrelic":  {"newrelic", "user", "newrelic_webhook", true},
	"dynatrace": {"dynatrace", "user", "dynatrace_webhook", true},
	"signoz":    {"signoz", "user", "signoz_webhook", true},
	"splunk":    {"splunk_observability_platform", "user", "splunk_webhook", true},
	// CubeAPM's admin API returns the full rule set in one call with no
	// permission filtering, so an absent rule can be trusted to mean deleted.
	"cubeapm": {"cubeapm", "user", "cubeapm_webhook", true},
	// Kibana filters _find by the caller's Kibana feature privileges and still
	// returns 200 — a credential holding feature_stackAlerts.read but not
	// feature_logs.read sees a subset, with `total` reporting only the visible
	// rules. There is no signal to distinguish that from rules having been
	// deleted, so deletions are never reconciled for this source.
	"elasticsearch": {"ES", "user", "elasticsearch_webhook", false},
}

// SyncableProviders lists the source values SyncProviderRules accepts, whether
// or not the underlying provider implements listing.
func SyncableProviders() []string {
	out := make([]string, 0, len(syncableProviders))
	for source := range syncableProviders {
		out = append(out, source)
	}
	return out
}

// SyncProviderRulesRequest is the payload of the eventrules_sync_provider_rules
// action. An empty Sources syncs every provider that supports listing, so a
// caller does not have to know which ones do.
type SyncProviderRulesRequest struct {
	AccountId string   `json:"account_id"`
	Sources   []string `json:"sources"`
}

// SyncProviderRulesForAccount runs SyncProviderRules for each requested source.
//
// A provider that fails — no integration configured, bad credentials, no list
// support — records the error on its own result and does not abort the others;
// with an empty Sources the caller is asking "sync whatever you can", and one
// unconfigured provider must not hide the rules of the ones that work. Only an
// invalid request returns a top-level error.
func SyncProviderRulesForAccount(sc *security.RequestContext, req SyncProviderRulesRequest) ([]ProviderSyncResult, error) {
	if req.AccountId == "" {
		return nil, fmt.Errorf("account_id is required")
	}
	if sc == nil || sc.GetSecurityContext() == nil {
		return nil, fmt.Errorf("security context is required")
	}

	sources := req.Sources
	explicit := len(sources) > 0
	if !explicit {
		sources = SyncableProviders()
	}

	results := make([]ProviderSyncResult, 0, len(sources))
	for _, source := range sources {
		entry, known := syncableProviders[source]
		if !known {
			// An explicitly requested unknown source is a caller error worth
			// reporting; when we expanded the list ourselves it cannot happen.
			results = append(results, ProviderSyncResult{
				Provider:  source,
				AccountId: req.AccountId,
				Error:     fmt.Sprintf("rule sync is not supported for source %q", source),
			})
			continue
		}
		// Skip providers with no list implementation unless the caller named
		// them, so the default "sync everything" call stays quiet.
		if !explicit && !alertrule.SupportsListing(entry.provider, entry.providerSource) {
			continue
		}

		result, err := SyncProviderRules(sc, req.AccountId, source)
		if err != nil {
			result.Error = err.Error()
			sc.GetLogger().Warn("rule sync: provider failed",
				"source", source, "account_id", req.AccountId, "error", err)
		}
		results = append(results, result)
	}
	return results, nil
}

// SyncProviderRules pulls every alert rule the provider has configured for the
// account and upserts it into event_rules.
//
// It deliberately does NOT go through CreateEventRule: that path calls
// alertrule.CreateAlertRule for external sources, which would push a brand-new
// monitor back into the provider for every rule we just read. This writes
// straight through upsertEventRule, which is a pure database upsert.
//
// Rules that have disappeared upstream are disabled rather than deleted — a
// row may already be referenced by an agent playbook or a workflow trigger, and
// a transient provider outage that returned a short list must not destroy them.
func SyncProviderRules(sc *security.RequestContext, accountId, source string) (ProviderSyncResult, error) {
	result := ProviderSyncResult{Provider: source, AccountId: accountId}

	if accountId == "" {
		return result, fmt.Errorf("account_id is required")
	}
	entry, ok := syncableProviders[source]
	if !ok {
		return result, fmt.Errorf("rule sync is not supported for source %q", source)
	}
	// Guarded rather than assumed: everything below dereferences sc for the
	// tenant id, the logger, and the provider call.
	if sc == nil || sc.GetSecurityContext() == nil {
		return result, fmt.Errorf("security context is required")
	}
	provider, providerSource := entry.provider, entry.providerSource
	// What we write to event_rules.source, which is no longer the same string
	// the caller passed in (`datadog` -> `datadog_webhook`).
	ruleSource := entry.ruleSource

	// A partial listing still carries usable rules; only deletion reconciliation
	// is unsafe, because anything the provider did not return would look
	// deleted. Upsert what we got and leave existing rows alone.
	rules, err := alertrule.ListAlertRules(sc, provider, providerSource, accountId)
	complete := true
	if errors.Is(err, alertrule.ErrIncompleteListing) {
		complete = false
		result.Error = err.Error()
		sc.GetLogger().Warn("rule sync: provider listing incomplete, skipping deletion reconciliation",
			"source", source, "account_id", accountId, "fetched", len(rules))
	} else if err != nil {
		return result, err
	}
	result.Fetched = len(rules)

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return result, fmt.Errorf("failed to get database manager: %w", err)
	}
	tenantId := sc.GetSecurityContext().GetTenantId()

	// event_rules.source is FK-constrained; register the value once up front so
	// a provider that has never been seen for this tenant does not fail every row.
	if err := EnsureEventRuleSource(sc, ruleSource); err != nil {
		return result, fmt.Errorf("failed to register event rule source %q: %w", ruleSource, err)
	}

	seen := make([]string, 0, len(rules))
	for _, rule := range rules {
		// The unique key is (account, tenant, source, alert), so a rule with no
		// name has nothing to key on and would collide with every other unnamed
		// rule from the same provider.
		if rule.Name == "" {
			result.Skipped++
			sc.GetLogger().Warn("rule sync: skipping provider rule with no name",
				"source", source, "external_rule_id", rule.ExternalRuleId)
			continue
		}

		// Recorded before the upsert is attempted, not after it succeeds: `seen`
		// answers "does this rule still exist upstream?", which is true whether
		// or not our local write worked. Appending only on success would let a
		// transient database error make the rule look deleted and get it
		// disabled below.
		seen = append(seen, rule.Name)

		annotations := rule.Annotations
		if annotations == nil {
			annotations = map[string]string{}
		}
		labels := rule.Labels
		if labels == nil {
			labels = map[string]string{}
		}

		_, _, err := upsertEventRule(dbms, annotations, labels,
			rule.Name, rule.Query, rule.Duration,
			accountId, tenantId,
			ruleSource, "alert", normalizeSyncSeverity(rule.Severity), rule.Enabled,
			rule.AlertType, provider, providerSource,
			rule.ExternalRuleId, rule.ProviderConfig)
		if err != nil {
			// One malformed rule must not abort the whole sync.
			result.Skipped++
			sc.GetLogger().Error("rule sync: failed to upsert provider rule",
				"source", source, "alert", rule.Name,
				"external_rule_id", rule.ExternalRuleId, "error", err)
			continue
		}
		result.Upserted++
	}

	// Only disable when the provider returned something AND we know we saw all
	// of it. An empty list is ambiguous — a provider with genuinely zero rules
	// is indistinguishable from a credential or API problem that surfaced as an
	// empty array — and a truncated list would disable every rule past the cut.
	if result.Fetched > 0 && complete && entry.reconcileDeletions {
		disabled, err := disableMissingProviderRules(dbms, accountId, tenantId, ruleSource, seen)
		if err != nil {
			sc.GetLogger().Error("rule sync: failed to disable removed provider rules",
				"source", source, "error", err)
		}
		result.Disabled = disabled
	}

	sc.GetLogger().Info("rule sync completed", "source", source, "account_id", accountId,
		"fetched", result.Fetched, "upserted", result.Upserted,
		"skipped", result.Skipped, "disabled", result.Disabled)
	return result, nil
}

// disableMissingProviderRules disables synced rows whose rule is no longer
// present upstream. Scoped to this (account, tenant, source) so it can never
// touch a webhook-ingested or user-authored row, and limited to rows that
// actually came from a sync (external_rule_id IS NOT NULL).
func disableMissingProviderRules(dbms *database.DatabaseManager, accountId, tenantId, source string, seen []string) (int, error) {
	res, err := dbms.Exec(`
		UPDATE event_rules
		SET enabled = false, updated_at = now()
		WHERE account_id = $1 AND tenant_id = $2 AND source = $3
		  AND external_rule_id IS NOT NULL
		  AND enabled = true
		  AND NOT (alert = ANY($4::text[]))`,
		accountId, tenantId, source, pq.Array(seen))
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

// normalizeSyncSeverity clamps a provider severity to event_rule_severity,
// which holds only 'critical' and 'warning'. A provider that reports anything
// else (or nothing) is treated as a warning rather than failing the insert on
// the foreign key.
func normalizeSyncSeverity(severity string) string {
	if severity == "critical" {
		return "critical"
	}
	return "warning"
}
