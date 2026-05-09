package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"nudgebee/services/tenant"
	"strings"
	"time"
)

// Tenant-config cache for per-event auto-analysis decisions.
//
// ProcessEvent is on the per-event hot path, so every avoidable DB read and
// per-event JSON parse adds up. The allowlist attribute changes very
// infrequently (tenant-admin config edits), so caching the already-parsed
// form for a few minutes trades a sliver of config-propagation latency for
// dramatically fewer DB hits + zero per-event json.Unmarshal.
//
// Storage: the shared “common.Cache“ namespace — bigcache-backed locally
// (or Redis, per CacheProvider config) with TTL + memory bounds already
// enforced by the namespace. No custom sync.Map / no unbounded growth.
//
// Encoding: we cache the PARSED+NORMALIZED allowlist as null-separated
// strings so the per-event code path only needs a single “bytes.Split“ —
// no JSON parse, no map build — and then a linear scan with “EqualFold“.
// A 0xFF sentinel byte distinguishes "configured → empty list (allow all)"
// from "configured → specific sources"; a cache miss falls through to DB.
const (
	eventAnalysisTenantCfgNamespace = "event_analysis_tenant_cfg"
	tenantCfgCacheTTL               = 5 * time.Minute
)

// emptyAllowlistSentinel marks a negative cache entry — tenant has no
// allowlist configured (or the configured list is empty/malformed), so the
// caller should allow all sources.
var emptyAllowlistSentinel = []byte{0xFF}

func init() {
	common.CacheCreateNamespace(
		eventAnalysisTenantCfgNamespace,
		common.CacheNamespaceWithExpiration(tenantCfgCacheTTL),
	)
}

// priorityRank maps priority strings to numeric ranks for comparison.
var priorityRank = map[string]int{
	"LOW":      1,
	"MEDIUM":   2,
	"HIGH":     3,
	"CRITICAL": 4,
}

// autoAnalysisRule represents a tenant-configured rule for auto-investigating events
// beyond the default HIGH-priority gate.
type autoAnalysisRule struct {
	AggregationKey string `json:"aggregation_key"`
	MinPriority    string `json:"min_priority"`
}

func ProcessEvent(ctx *security.RequestContext, event map[string]any) (err error) {

	priority, _ := event["priority"].(string)
	aggregationKey, _ := event["aggregation_key"].(string)

	tenantId, tenantIdOk := event["tenant"].(string)
	if !tenantIdOk {
		ctx.GetLogger().Error("llms: tenant is required")
		return nil
	}

	// Triage classified this event as suppressed — auto-investigation would
	// resurface noise the user explicitly silenced. Reads from the in-memory
	// map populated at consumer pickup, so it catches later firings of an
	// already-suppressed chain; a same-batch race with concurrent triage is
	// not covered here (would need ordered processors in PostProcessEvent).
	if nbStatus, _ := event["nb_status"].(string); strings.EqualFold(nbStatus, "SUPPRESSED") {
		return nil
	}

	// Default path: HIGH priority events are always eligible
	if priority != "HIGH" {
		// For non-HIGH events, check if tenant has configured auto-analysis rules for this aggregation key
		if !isEligibleByTenantRules(ctx, tenantId, aggregationKey, priority) {
			return nil
		}
	}

	// Consumer strips evidences from the map to avoid memory bloat;
	// check the has_evidences flag instead.
	hasEvidences, _ := event["has_evidences"].(bool)
	if !hasEvidences {
		return nil
	}

	// for now filter error logs
	if aggregationKey == "HighErrorCriticalLogs" || aggregationKey == "Anomaly" {
		return nil
	}

	// for api failures skip common codes
	if aggregationKey == "ApplicationAPIFailures" && event["labels"] != nil {
		labels, ok := event["labels"].(map[string]any)
		if !ok {
			labelStr, ok := event["labels"].(string)
			if !ok {
				return nil
			}
			labelMap := map[string]any{}
			err := json.Unmarshal([]byte(labelStr), &labelMap)
			if err != nil {
				return err
			}
			labels = labelMap
		}

		if status, ok := labels["status"]; ok {
			if status == "404" || status == "401" || status == "400" || status == "403" {
				return nil
			}
		} else {
			return nil
		}
	}

	if !config.Config.FeatureEventAutoAiSummaryEnabled {
		return nil
	}

	if !tenant.IsFeatureEnabledByDefault(ctx, tenantId, tenant.FEATURE_EVENT_AUTO_AI_SUMMARY) {
		return nil
	}

	// Tenant-scoped source allowlist. When configured, only events whose
	// ``source`` matches an entry are eligible for auto-analysis; when
	// unset or empty the legacy "allow all" behaviour is preserved.
	eventSource, _ := event["source"].(string)
	if !isSourceAllowedByTenant(ctx, tenantId, eventSource) {
		return nil
	}

	accountId, accountIdOk := event["cloud_account_id"].(string)
	if !accountIdOk {
		err = fmt.Errorf("workflow: cloud_account_id is missing or not a string")
		ctx.GetLogger().Error(err.Error())
		return err
	}

	id, idOk := event["id"].(string)
	if !idOk {
		err = fmt.Errorf("workflow: event id is missing or not a string")
		ctx.GetLogger().Error(err.Error())
		return err
	}

	ctx.GetLogger().Info("llms: publishing new event to llm-server for processing", "id", id, "account_id", accountId)
	message := map[string]any{
		"account_id": accountId,
		"event_id":   id,
	}

	err = common.MqPublish(config.Config.RabbitMqTroubleshootExchange, config.Config.RabbitMqTroubleshootQueue, message, common.MqPublishWithExpiration(1*time.Hour))
	if err != nil {
		ctx.GetLogger().Error("llms: error publishing message to queue", "error", err)
		return err
	}

	return nil
}

const tenantAttrEventAutoAnalysisRules = "event_auto_analysis_rules"

// isEligibleByTenantRules checks if a non-HIGH event matches a tenant-configured auto-analysis rule.
func isEligibleByTenantRules(ctx *security.RequestContext, tenantId string, aggregationKey string, priority string) bool {
	if aggregationKey == "" || priority == "" {
		return false
	}

	rulesJSON, found, err := tenant.GetTenantAttributeValueByTenantId(ctx, tenantId, tenantAttrEventAutoAnalysisRules)
	if err != nil {
		ctx.GetLogger().Error("llms: error fetching auto analysis rules", "error", err, "tenant", tenantId)
		return false
	}
	if !found || rulesJSON == "" {
		return false
	}

	var rules []autoAnalysisRule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		ctx.GetLogger().Error("llms: error parsing auto analysis rules", "error", err, "tenant", tenantId)
		return false
	}

	eventRank := priorityRank[strings.ToUpper(priority)]
	if eventRank == 0 {
		return false
	}

	for _, rule := range rules {
		if rule.AggregationKey == aggregationKey {
			minRank := priorityRank[strings.ToUpper(rule.MinPriority)]
			if minRank == 0 {
				continue
			}
			return eventRank >= minRank
		}
	}

	return false
}

const tenantAttrEventAutoAnalysisAllowedSources = "event_auto_analysis_allowed_sources"

// isSourceAllowedByTenant returns true when either the tenant has not
// configured an allowlist (implicit "allow all" — preserves legacy behaviour)
// or the event's source matches an entry in the configured allowlist.
//
// Fails open on DB / parse errors so a transient infra hiccup or a malformed
// attribute value doesn't silently disable auto-analysis for the tenant.
// Matching is case-insensitive and whitespace-trimmed on both sides so small
// UI / config typos don't drop events unexpectedly.
func isSourceAllowedByTenant(ctx *security.RequestContext, tenantId, source string) bool {
	cached, configured := getTenantAllowedSources(ctx, tenantId)
	if !configured {
		return true
	}
	// Allowlist is configured. An empty source can't match any entry under
	// strict-allowlist semantics — block without a per-event log to avoid
	// flooding on upstream producer regressions. (Follow-up: a counter
	// metric would be a better signal than a per-drop log.)
	targetBytes := bytes.ToLower(bytes.TrimSpace([]byte(source)))
	if len(targetBytes) == 0 {
		return false
	}
	// Cached form is null-separated, already lowercase + trimmed (normalized
	// at write time in ``getTenantAllowedSources``). Per-event cost: one
	// ``bytes.Split`` (no allocation of string copies) + ``bytes.Equal``
	// per entry. No ``strings.EqualFold``, no ``json.Unmarshal``.
	for _, s := range bytes.Split(cached, []byte{0x00}) {
		if bytes.Equal(s, targetBytes) {
			return true
		}
	}
	return false
}

// getTenantAllowedSources returns the tenant's configured source allowlist
// as a null-separated, lowercase, trimmed byte blob suitable for per-event
// “bytes.Split“ + “bytes.Equal“ comparison without per-call string
// allocation. The boolean is true only when an allowlist is configured AND
// non-empty after normalization; false means "no allowlist applies, allow
// all sources".
//
// Results are cached in “common.Cache“ (TTL- and memory-bounded). The
// cached form is already-parsed, pre-normalized, and lowercased, so the
// per-event hot path skips the DB query, json.Unmarshal, and any string
// normalization.
//
// Fails open on any DB / parse error (returns "not configured") so a bad
// attribute value can never silently disable analysis for the tenant.
func getTenantAllowedSources(ctx *security.RequestContext, tenantId string) ([]byte, bool) {
	if cached, hit := common.CacheGet(eventAnalysisTenantCfgNamespace, tenantId); hit {
		if bytes.Equal(cached, emptyAllowlistSentinel) {
			return nil, false
		}
		return cached, true
	}

	raw, found, err := tenant.GetTenantAttributeValueByTenantId(ctx, tenantId, tenantAttrEventAutoAnalysisAllowedSources)
	if err != nil {
		// Don't cache errors — next call retries.
		ctx.GetLogger().Error("llms: error fetching allowed-sources attribute", "error", err, "tenant", tenantId)
		return nil, false
	}
	if !found || strings.TrimSpace(raw) == "" {
		_ = common.CacheSet(eventAnalysisTenantCfgNamespace, tenantId, emptyAllowlistSentinel)
		return nil, false
	}

	var parsed []string
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		ctx.GetLogger().Error("llms: malformed allowed-sources attribute", "error", err, "tenant", tenantId)
		_ = common.CacheSet(eventAnalysisTenantCfgNamespace, tenantId, emptyAllowlistSentinel)
		return nil, false
	}

	// Normalize each entry: trim + lowercase. Storing pre-lowercased lets
	// the per-event path use ``bytes.Equal`` against a lowercased target
	// rather than ``strings.EqualFold``.
	normalized := make([]string, 0, len(parsed))
	for _, s := range parsed {
		if t := strings.ToLower(strings.TrimSpace(s)); t != "" {
			normalized = append(normalized, t)
		}
	}
	if len(normalized) == 0 {
		_ = common.CacheSet(eventAnalysisTenantCfgNamespace, tenantId, emptyAllowlistSentinel)
		return nil, false
	}

	val := []byte(strings.Join(normalized, "\x00"))
	_ = common.CacheSet(eventAnalysisTenantCfgNamespace, tenantId, val)
	return val, true
}
