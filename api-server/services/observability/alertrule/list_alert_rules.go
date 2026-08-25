package alertrule

import (
	"errors"
	"fmt"

	"nudgebee/services/security"
)

// ErrIncompleteListing is returned ALONGSIDE a partial result set when a
// provider could not be enumerated in full (a page cap, a truncated response).
// The rules returned are still valid and safe to upsert; what is not safe is
// concluding that anything absent from them has been deleted upstream.
//
// Callers must therefore treat it as "upsert these, but do not reconcile
// deletions" rather than as a hard failure.
var ErrIncompleteListing = errors.New("alert rule listing is incomplete")

// ExternalAlertRule is one alert rule as it exists in the provider. It is the
// read counterpart of AlertRuleConfig: the same shape plus the provider's own
// identifier, which is what makes a synced rule addressable for later
// update/delete.
type ExternalAlertRule struct {
	ExternalRuleId string            `json:"external_rule_id"`
	Name           string            `json:"name"`
	AlertType      string            `json:"alert_type"` // "metric" or "log"
	Query          string            `json:"query"`      // provider-native query expression
	Severity       string            `json:"severity"`   // critical | warning (event_rule_severity)
	Duration       string            `json:"duration"`
	Annotations    map[string]string `json:"annotations"`
	Labels         map[string]string `json:"labels"`
	Enabled        bool              `json:"enabled"`
	// ProviderConfig carries provider-specific fields worth keeping on the
	// event_rules row (monitor type, folder, dashboard link, …).
	ProviderConfig map[string]any `json:"provider_config"`
}

// AlertRuleLister is implemented by providers that can enumerate their alert
// rules.
//
// Deliberately separate from AlertRuleSource rather than another method on it:
// ten providers implement AlertRuleSource today and only some have a usable
// list API, so folding List into that interface would force eight stub methods
// that exist only to satisfy the compiler. Callers type-assert instead, and
// ListAlertRules reports a clear error for providers that do not implement it.
type AlertRuleLister interface {
	ListAlertRules(ctx *security.RequestContext, accountId string) ([]ExternalAlertRule, error)
}

// ListAlertRules enumerates the alert rules configured in the external system
// for one account. Providers that cannot be listed return an explicit
// "not supported" error rather than an empty slice, so a caller can tell
// "nothing configured" apart from "we cannot see it".
func ListAlertRules(ctx *security.RequestContext, provider, providerSource, accountId string) ([]ExternalAlertRule, error) {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return nil, err
	}
	lister, ok := source.(AlertRuleLister)
	if !ok {
		return nil, fmt.Errorf("listing alert rules is not supported for provider %s/%s", provider, providerSource)
	}
	return lister.ListAlertRules(ctx, accountId)
}

// SupportsListing reports whether a provider can enumerate its rules, so a
// caller can skip it without provoking an error.
func SupportsListing(provider, providerSource string) bool {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return false
	}
	_, ok := source.(AlertRuleLister)
	return ok
}
