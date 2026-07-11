// Package engine embeds the Bifrost core as an in-process library. The gateway
// owns the HTTP edge and governance (auth, rate-limit, metering); Bifrost core
// owns the provider clients, cloud-credential signing, native passthrough
// fidelity, and per-request usage extraction. Credentials are resolved by the NB
// Account (GetKeysForProvider) — a released bifrost-core mechanism, so we depend
// on a normal published version, not a fork.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Engine holds the embedded Bifrost core client.
type Engine struct {
	Client *bifrost.Bifrost
}

// New initialises the embedded core with the NB account. It accepts one or more
// operator provider creds; each valid one becomes a credential keyed by provider,
// so multiple providers (e.g. Anthropic + Gemini) can be served at once. The
// account returns the right key per addressed provider via GetKeysForProvider.
//
// Only Account is required by bifrost.Init; Logger (nil => core default), Tracer
// (nil => no-op), MCP and KVStore (nil => disabled) are left unset for now.
func New(ctx context.Context, providerCreds []ProviderCredsConfig) (*Engine, error) {
	creds := make(map[schemas.ModelProvider]providerCred)
	providers := make([]schemas.ModelProvider, 0, len(providerCreds))
	for _, pc := range providerCreds {
		provider, cred, ok := buildCred(pc)
		if !ok {
			// A provider was named but produced no usable credential — warn loudly
			// rather than boot into a state where every request fails opaquely.
			if strings.TrimSpace(pc.Provider) != "" {
				slog.Warn("engine: provider configured but no usable credential; requests to it will fail",
					"provider", pc.Provider)
			}
			continue
		}
		// Dedupe: a provider may be configured twice (legacy block + per-provider
		// key). Last cred wins; the provider is listed once.
		if _, exists := creds[provider]; !exists {
			providers = append(providers, provider)
		}
		creds[provider] = cred
	}
	slog.Info("engine: initialised", "providers", providers)

	client, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account: newNBAccount(creds, providers),
	})
	if err != nil {
		return nil, fmt.Errorf("engine: bifrost init: %w", err)
	}
	return &Engine{Client: client}, nil
}

// Shutdown releases core resources (drains queues, closes providers).
func (e *Engine) Shutdown() {
	if e.Client != nil {
		e.Client.Shutdown()
	}
}
