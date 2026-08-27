package core

import (
	"context"
	"strings"

	"nudgebee/services/security"
)

// Previewing a configuration that has not been saved yet.
//
// Every log provider resolves its credentials the same way — some
// Get<Provider>Config(ctx, accountId) that bottoms out in ListIntegrationConfigs.
// That is what makes "list this backend's fields using the values currently typed
// into the form" a single override rather than one probe per provider: substitute
// the values for that one lookup and every provider's existing query path runs
// against them unchanged.
//
// On the trust boundary: this grants nothing new. TestIntegrationConnectionByConfig
// and integrations_list_es_indexes already accept config values from a tenant admin
// and connect to them — that is what Test Connection is. This reuses that boundary
// rather than widening it, and is set only by read-only, tenant-admin-gated probe
// handlers.

type configOverrideKeyType struct{}

// configOverrideKey is a private struct type so nothing outside this package can
// collide with it or forge an override onto a context.
var configOverrideKey = configOverrideKeyType{}

// ConfigOverride substitutes caller-supplied config values for ONE
// (AccountId, IntegrationName) lookup.
type ConfigOverride struct {
	AccountId       string
	IntegrationName string
	// Source is stamped onto the synthetic integration so provider config getters
	// that filter on Source == "user" still find it.
	Source string
	// Values must already be decrypted — see core.DecryptConfigValues.
	Values []IntegrationConfigValue
}

// WithConfigOverride returns a context in which ListIntegrationConfigs answers the
// matching lookup from o.Values instead of the database. Everything else about the
// request — tenant, permissions, other integrations — is untouched.
func WithConfigOverride(ctx *security.RequestContext, o ConfigOverride) *security.RequestContext {
	if ctx == nil {
		return nil
	}
	return security.NewRequestContext(
		context.WithValue(ctx.GetContext(), configOverrideKey, o),
		ctx.GetSecurityContext(),
		ctx.GetLogger(),
		ctx.GetTracer(),
		ctx.GetMeter(),
	)
}

// configOverrideFor returns the override carried by ctx, but only when it targets
// exactly this (accountId, integrationName).
//
// Matching on BOTH is the point. A bare "override is active" flag would also answer
// every unrelated integration lookup the same request happens to make — a log-field
// probe would start feeding one provider's credentials to another's config getter.
func configOverrideFor(ctx *security.RequestContext, accountId, integrationName string) (ConfigOverride, bool) {
	if ctx == nil {
		return ConfigOverride{}, false
	}
	c := ctx.GetContext()
	if c == nil {
		return ConfigOverride{}, false
	}
	o, ok := c.Value(configOverrideKey).(ConfigOverride)
	if !ok {
		return ConfigOverride{}, false
	}
	if o.AccountId == "" || o.IntegrationName == "" {
		return ConfigOverride{}, false
	}
	if o.AccountId != accountId || !strings.EqualFold(o.IntegrationName, integrationName) {
		return ConfigOverride{}, false
	}
	return o, true
}

// syntheticDto renders the override as the integration record the caller would have
// got had they saved the form. It carries Source/Type/Configs because the provider
// config getters filter on Source and read Configs; Id stays empty, which is what
// marks it as never having been persisted.
func (o ConfigOverride) syntheticDto() IntegrationDto {
	source := o.Source
	if source == "" {
		source = "user"
	}
	return IntegrationDto{
		Name:    o.IntegrationName,
		Type:    o.IntegrationName,
		Source:  source,
		Configs: o.Values,
		Tags:    map[string]any{},
	}
}
