package engine

import (
	"context"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/config"
)

// nbAccount is the Bifrost Account backing the embedded core. It resolves the
// provider credential per request via GetKeysForProvider(ctx) — the released
// bifrost-core mechanism for dynamic key selection — so we depend on a normal
// released version, not a fork.
//
//   - GetConfiguredProviders declares which providers the gateway serves.
//   - GetKeysForProvider returns the credential for the addressed provider. The
//     ctx can carry NB identity (set before the request); by default creds are
//     per-provider from operator config.
//   - GetConfigForProvider returns a config for every provider the gateway can
//     serve (released core does NOT auto-initialise on a nil config — it errors
//     "config is nil"). We seed DefaultNetworkConfig so timeouts/retries are set,
//     and leave BaseURL empty so the provider client fills its own default; a
//     base-URL override wins when configured. This covers operator-configured
//     providers AND any standard bifrost provider a tenant supplies a BYO key for
//     (the per-request DirectKey needs a provider config to run, and the operator
//     is not required to have pre-configured the provider — see GetConfigForProvider).
type nbAccount struct {
	creds     map[schemas.ModelProvider]providerCred
	providers []schemas.ModelProvider
}

func newNBAccount(creds map[schemas.ModelProvider]providerCred, providers []schemas.ModelProvider) *nbAccount {
	return &nbAccount{creds: creds, providers: providers}
}

func (a *nbAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return a.providers, nil
}

func (a *nbAccount) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	if c, ok := a.creds[provider]; ok {
		return []schemas.Key{c.key}, nil
	}
	return []schemas.Key{}, nil
}

func (a *nbAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	// Operator-configured provider: honor its base-URL override if set.
	if c, ok := a.creds[provider]; ok {
		return defaultProviderConfig(c.endpoint), nil
	}
	// Not operator-configured, but a standard bifrost provider — a tenant may supply
	// a BYO key for it per request (injected as a DirectKey). Core builds the provider
	// lazily on first request via this config, so returning nil here would fail every
	// tenant-BYO request with "config is nil for provider" even though a valid tenant
	// key exists. A default config (empty BaseURL → the client's own default endpoint)
	// is enough for the api-key providers the tenant resolver produces; a request with
	// no operator AND no tenant key is already rejected upstream by the resolver stage,
	// so this never serves a provider keyless.
	if bifrost.IsStandardProvider(provider) {
		return defaultProviderConfig(""), nil
	}
	// Unknown/non-standard provider (e.g. a typo'd llm_provider) — nil lets core reject
	// cleanly rather than us fabricating an endpoint for a provider it can't build.
	return nil, nil
}

// defaultProviderConfig builds a provider config seeded with DefaultNetworkConfig
// (so timeouts/retries are set). BaseURL is left empty unless an override endpoint
// is given, so the provider client fills its own default.
func defaultProviderConfig(endpoint string) *schemas.ProviderConfig {
	nc := schemas.DefaultNetworkConfig
	if endpoint != "" {
		nc.BaseURL = endpoint
	}
	// Operator opt-in to reach RFC1918 private IPs (self-hosted / internal endpoints). Off
	// by default; link-local/metadata stays blocked by Bifrost's dialer regardless.
	nc.AllowPrivateNetwork = config.Config.AllowPrivateEndpoints
	return &schemas.ProviderConfig{
		NetworkConfig:            nc,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}
}
