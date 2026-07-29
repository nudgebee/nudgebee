package engine

import (
	"context"

	"github.com/maximhq/bifrost/core/schemas"
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
//   - GetConfigForProvider returns a config for every configured provider (released
//     core does NOT auto-initialise on a nil config — it errors "config is nil").
//     We seed DefaultNetworkConfig so timeouts/retries are set, and leave BaseURL
//     empty so the provider client fills its own default; a base-URL override wins
//     when configured.
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
	c, ok := a.creds[provider]
	if !ok {
		// Not a configured provider — nil lets core reject cleanly for an
		// unconfigured provider rather than us fabricating an endpoint.
		return nil, nil
	}
	// Released core does NOT auto-initialise on a nil config (it errors "config
	// is nil for provider"); return a real config for every configured provider.
	// Seed DefaultNetworkConfig so timeouts/retries are set; leave BaseURL empty
	// so the provider client fills its own default, unless an override is set.
	nc := schemas.DefaultNetworkConfig
	if c.endpoint != "" {
		nc.BaseURL = c.endpoint
	}
	return &schemas.ProviderConfig{
		NetworkConfig:            nc,
		ConcurrencyAndBufferSize: schemas.DefaultConcurrencyAndBufferSize,
	}, nil
}
