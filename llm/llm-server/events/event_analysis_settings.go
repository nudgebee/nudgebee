package events

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// eventAnalysisSettingsCacheNS caches the raw setting per account. The repository
// is built on every analysis request, including each UI poll, so the setting is
// read at most once per TTL per account; an admin's change applies within it.
const (
	eventAnalysisSettingsCacheNS  = "event_analysis_account_settings"
	eventAnalysisSettingsCacheTTL = time.Minute
)

// noSettingsSentinel caches "the account has no event_analysis row".
var noSettingsSentinel = []byte{0xFF}

func init() {
	common.CacheCreateNamespace(eventAnalysisSettingsCacheNS, common.CacheNamespaceWithExpiration(eventAnalysisSettingsCacheTTL))
}

// EventAnalysisSettingsAttr is the cloud_account_attrs key the account settings
// modal writes the per-account event analysis configuration under.
const EventAnalysisSettingsAttr = "event_analysis"

// EventAnalysisSettings is the per-account event analysis configuration. Every
// field is optional; an account that never saved the settings behaves exactly
// as before they existed.
//
// ExcludedNamespaces is enforced by api-server before an event is queued for
// automatic analysis; it is carried here only so the shape matches the stored
// JSON.
type EventAnalysisSettings struct {
	// ReuseRelated false means a new event never reuses the analysis of an
	// earlier event with the same fingerprint. Nil means true.
	ReuseRelated *bool `json:"reuse_related"`
	// ReuseWindowHours overrides config.EventAnalysisFreshnessHours for this
	// account. Nil or <= 0 keeps the global value.
	ReuseWindowHours      *int     `json:"reuse_window_hours"`
	AlwaysFreshNamespaces []string `json:"always_fresh_namespaces"`
	ExcludedNamespaces    []string `json:"excluded_namespaces"`
}

// ReusesRelated reports whether a new event may reuse a related event's analysis.
func (s EventAnalysisSettings) ReusesRelated() bool {
	return s.ReuseRelated == nil || *s.ReuseRelated
}

// IsAlwaysFresh reports whether events in namespace must get their own analysis.
func (s EventAnalysisSettings) IsAlwaysFresh(namespace string) bool {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return false
	}
	for _, ns := range s.AlwaysFreshNamespaces {
		if strings.TrimSpace(ns) == namespace {
			return true
		}
	}
	return false
}

// NewEventAnalysisRepositoryForAccount returns a repository whose reuse window
// honours the account's settings. Use it wherever the account is known, so the
// freshness checks on every reuse gate agree with each other.
func NewEventAnalysisRepositoryForAccount(ctx *security.RequestContext, dbManager *common.DatabaseManager, accountId string) *EventAnalysisRepository {
	r := NewEventAnalysisRepository(dbManager)
	r.settings = r.loadAccountSettings(ctx, accountId)
	if h := r.settings.ReuseWindowHours; h != nil && *h > 0 {
		r.analysisFreshness = time.Duration(*h) * time.Hour
	}
	return r
}

// Settings returns the account settings the repository was built with; the zero
// value (defaults) for a repository built without an account.
func (r *EventAnalysisRepository) Settings() EventAnalysisSettings {
	return r.settings
}

// loadAccountSettings fails open: a missing, unreadable or malformed row yields
// the defaults, because a bad setting must not stop event analysis. Read errors
// are not cached, so the next request retries.
func (r *EventAnalysisRepository) loadAccountSettings(ctx *security.RequestContext, accountId string) EventAnalysisSettings {
	raw, hit := common.CacheGet(eventAnalysisSettingsCacheNS, accountId)
	if !hit {
		var value string
		err := r.dbManager.Db.Get(&value,
			`SELECT value FROM cloud_account_attrs WHERE cloud_account_id = $1 AND name = $2`,
			accountId, EventAnalysisSettingsAttr)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			raw = noSettingsSentinel
		case err != nil:
			ctx.GetLogger().Warn("analyzer: reading event analysis settings, using defaults", "account_id", accountId, "error", err)
			return EventAnalysisSettings{}
		default:
			raw = []byte(value)
		}
		_ = common.CacheSet(eventAnalysisSettingsCacheNS, accountId, raw)
	}
	if bytes.Equal(raw, noSettingsSentinel) || len(bytes.TrimSpace(raw)) == 0 {
		return EventAnalysisSettings{}
	}
	var settings EventAnalysisSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		ctx.GetLogger().Warn("analyzer: event analysis settings are not valid JSON, using defaults", "account_id", accountId, "error", err)
		return EventAnalysisSettings{}
	}
	return settings
}
