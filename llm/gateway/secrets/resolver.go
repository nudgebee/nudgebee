// Package secrets resolves a tenant's provider credential from the NB integration
// tables so the gateway can inject per-tenant BYO keys per request. It is the
// DB-backed implementation of the proxy pipeline's CredResolver seam.
package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/common"
	"nudgebee/llm-gateway/engine"
)

const cacheTTL = 30 * time.Minute

// Resolver resolves a tenant's provider API key from `integrations` (type='llm',
// enabled) + `integration_config_values`, caching per tenant. It implements the
// proxy CredResolver interface, so a resolved key is injected for the request and
// the operator/account default is used only as a fallback.
//
// Grain is the TENANT — the gateway identity carries no account, so it reads ALL of
// the tenant's enabled llm integrations and matches by provider; per-account key
// selection is out of scope for P1. Only api-key providers are handled today; cloud
// BYO (Bedrock/Vertex structured creds) follows when those lanes are added.
//
// Fail-soft throughout: an unreachable DB, an undecryptable value, or a missing key
// yields "no tenant credential" and falls back to the operator default rather than
// erroring the request.
type Resolver struct {
	db func() (*common.DatabaseManager, error)

	mu    sync.RWMutex
	cache map[string]entry // tenantID → resolved provider→apikey
}

type entry struct {
	providers map[schemas.ModelProvider]string
	at        time.Time
}

// NewResolver builds a tenant-config resolver backed by the shared metastore.
func NewResolver() *Resolver {
	return &Resolver{
		db:    func() (*common.DatabaseManager, error) { return common.GetDatabaseManager(common.Metastore) },
		cache: map[string]entry{},
	}
}

// Resolve returns the tenant's api key for the addressed provider. ok=false means
// no tenant-specific credential — the caller falls back to the operator default.
func (r *Resolver) Resolve(ctx context.Context, provider schemas.ModelProvider, id auth.Identity) (schemas.Key, bool) {
	if id.TenantID == "" {
		return schemas.Key{}, false
	}
	apiKey := r.load(ctx, id.TenantID)[provider]
	if apiKey == "" {
		return schemas.Key{}, false
	}
	return schemas.Key{
		ID:     string(provider) + "-tenant",
		Models: schemas.WhiteList{"*"}, // wildcard: core denies empty-Models keys by default
		Value:  schemas.SecretVar{Val: apiKey},
	}, true
}

func (r *Resolver) load(ctx context.Context, tenantID string) map[schemas.ModelProvider]string {
	r.mu.RLock()
	e, ok := r.cache[tenantID]
	r.mu.RUnlock()
	if ok && time.Since(e.at) < cacheTTL {
		return e.providers
	}
	providers, err := r.fetch(ctx, tenantID)
	if err != nil {
		// Do NOT cache a transient failure — the tenant would be pinned to the
		// operator default for the whole TTL even after the DB recovers. Return the
		// fallback and retry on the next request.
		slog.Error("secrets: tenant llm-config fetch failed; using operator default", "error", err, "tenant_id", tenantID)
		return map[schemas.ModelProvider]string{}
	}
	r.mu.Lock()
	r.cache[tenantID] = entry{providers: providers, at: time.Now()}
	r.mu.Unlock()
	return providers
}

func (r *Resolver) fetch(ctx context.Context, tenantID string) (map[schemas.ModelProvider]string, error) {
	db, err := r.db()
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT icv.integration_id::text AS integration_id, icv.name::text AS name,
		       COALESCE(icv.value, '') AS value, COALESCE(icv.is_encrypted, false) AS is_encrypted
		FROM integrations i
		JOIN integration_config_values icv ON icv.integration_id = i.id
		WHERE i.type = 'llm' AND i.status = 'enabled' AND i.tenant_id = $1`
	rows, err := db.Db.QueryxContext(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Group config values by integration, decrypting encrypted secrets.
	byIntegration := map[string]map[string]string{}
	for rows.Next() {
		var integrationID, name, value string
		var isEnc bool
		if err := rows.Scan(&integrationID, &name, &value, &isEnc); err != nil {
			// A malformed row is a systematic (schema) issue, not transient — skip it
			// rather than fail the whole tenant; a mid-stream error is caught below.
			slog.Error("secrets: scanning tenant config row", "error", err)
			continue
		}
		if name == "" {
			continue
		}
		if isEnc && value != "" {
			dec, derr := common.Decrypt(value)
			if derr != nil {
				slog.Warn("secrets: could not decrypt config value; skipping", "name", name, "tenant_id", tenantID)
				continue
			}
			value = dec
		}
		m := byIntegration[integrationID]
		if m == nil {
			m = map[string]string{}
			byIntegration[integrationID] = m
		}
		m[name] = value
	}
	if err := rows.Err(); err != nil {
		// A mid-iteration failure (e.g. connection loss) — return the error so the
		// partial result is NOT cached and the next request retries.
		return nil, fmt.Errorf("secrets: iterating tenant config rows: %w", err)
	}
	return buildProviderKeys(byIntegration), nil
}

// buildProviderKeys reduces per-integration config maps to provider→api-key,
// keyed by the normalized provider. Integrations without a provider or api key are
// skipped; when a tenant has several integrations for the same provider, the last
// one wins (an accepted ambiguity at the tenant grain).
func buildProviderKeys(byIntegration map[string]map[string]string) map[schemas.ModelProvider]string {
	out := map[schemas.ModelProvider]string{}
	for _, cfg := range byIntegration {
		provider := engine.NormalizeProvider(cfg["llm_provider"])
		apiKey := cfg["llm_provider_api_key"]
		if provider == "" || apiKey == "" {
			continue
		}
		out[provider] = apiKey
	}
	return out
}
