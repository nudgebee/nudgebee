package alertrule

import (
	"fmt"

	"nudgebee/services/security"
)

// AlertRuleGetter is implemented by providers that can read one alert rule back
// by the id Nudgebee stored for it.
//
// Deliberately separate from AlertRuleSource, and discovered by type assertion,
// for the same reason AlertRuleLister is: thirteen providers implement
// AlertRuleSource and folding a read into it would force a dozen stub methods
// that exist only to satisfy the compiler.
//
// It is separate from AlertRuleLister too, because the two capabilities are
// genuinely independent. SolarWinds can be read by id but never enumerated: its
// API ships no list operation, so Nudgebee can show the true definition of an
// alert it already knows about while being permanently unable to discover one it
// has not seen. A provider that can be listed usually gets its definitions from
// SyncProviderRules instead, and has no need of this.
type AlertRuleGetter interface {
	GetAlertRule(ctx *security.RequestContext, accountId, externalRuleId string) (*ExternalAlertRule, error)
}

// GetAlertRule reads one alert rule from the external system.
//
// Providers that cannot be read return an explicit "not supported" error rather
// than an empty rule, so a caller can tell "this provider cannot answer" apart
// from "the rule has no settings".
func GetAlertRule(ctx *security.RequestContext, provider, providerSource, accountId, externalRuleId string) (*ExternalAlertRule, error) {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return nil, err
	}
	getter, ok := source.(AlertRuleGetter)
	if !ok {
		return nil, fmt.Errorf("reading a single alert rule is not supported for provider %s/%s", provider, providerSource)
	}
	return getter.GetAlertRule(ctx, accountId, externalRuleId)
}

// SupportsGet reports whether a provider can read one rule back, so a caller can
// skip it without provoking an error.
func SupportsGet(provider, providerSource string) bool {
	source, err := getAlertRuleSource(provider, providerSource)
	if err != nil {
		return false
	}
	_, ok := source.(AlertRuleGetter)
	return ok
}
