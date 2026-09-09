package core

import (
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/common"
)

// Model token ceilings live in the llm_model_pricing catalog (V878) so they are
// editable without a release. Resolution order for max output tokens:
//
//  1. per-account config key llm_model_max_output_tokens (DB integration
//     config → ENV) via GetLLMModelIntConfig — the deployment override
//  2. llm_model_pricing row (tenant row wins over built-in) — the catalog default
//  3. 0 — the caller applies its conservative floor and warns
//
// Max context follows the same catalog with the existing config field
// (llm_model_context_size) above it and the code model map below it.

type modelTokenLimits struct {
	MaxOutput  int
	MaxContext int
}

const modelLimitsCacheTTL = 5 * time.Minute

type modelLimitsEntry struct {
	byKey map[string]modelTokenLimits // "provider:model", lowercased
	at    time.Time
}

var (
	modelLimitsMu    sync.RWMutex
	modelLimitsCache = map[string]modelLimitsEntry{} // tenantId ("" = built-ins) → catalog
)

// fetchModelTokenLimits loads the tenant-collapsed limits catalog. Package
// variable so tests can substitute a fake catalog.
var fetchModelTokenLimits = func(tenantId string) (map[string]modelTokenLimits, error) {
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	// DISTINCT ON + tenant_id NULLS LAST keeps the tenant's own row when it
	// shadows a built-in one — same shape as GetConversationCosts.
	query := `SELECT DISTINCT ON (provider_name, model_name)
	    provider_name, model_name,
	    COALESCE(max_output_tokens, 0), COALESCE(max_context_tokens, 0)
	FROM llm_model_pricing
	WHERE tenant_id IS NULL
	ORDER BY provider_name, model_name`
	args := []any{}
	if tenantId != "" {
		query = `SELECT DISTINCT ON (provider_name, model_name)
		    provider_name, model_name,
		    COALESCE(max_output_tokens, 0), COALESCE(max_context_tokens, 0)
		FROM llm_model_pricing
		WHERE tenant_id IS NULL OR tenant_id = $1
		ORDER BY provider_name, model_name, tenant_id NULLS LAST`
		args = append(args, tenantId)
	}
	rows, err := dbms.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]modelTokenLimits{}
	for rows.Next() {
		var provider, model string
		var maxOut, maxCtx int
		if err := rows.Scan(&provider, &model, &maxOut, &maxCtx); err != nil {
			return nil, err
		}
		prov := strings.ToLower(strings.TrimSpace(provider))
		m := strings.ToLower(strings.TrimSpace(model))
		out[prov+":"+m] = modelTokenLimits{MaxOutput: maxOut, MaxContext: maxCtx}
		// Also index the canonical form so provider-qualified ids (Bedrock
		// "us.anthropic.claude-sonnet-4-6-...-v1:0") can meet the bare dotted
		// rows the seed uses. A raw key always wins over an alias, and the
		// first alias wins over later ones — rows arrive sorted, so this is
		// deterministic.
		if c := canonicalModelID(m); c != m {
			if _, exists := out[prov+":"+c]; !exists {
				out[prov+":"+c] = modelTokenLimits{MaxOutput: maxOut, MaxContext: maxCtx}
			}
		}
	}
	// Never hand back a partially populated catalog: on an iteration error the
	// caller must keep serving its stale entry instead of caching this one.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadModelLimitsCatalog(tenantId string) map[string]modelTokenLimits {
	modelLimitsMu.RLock()
	e, ok := modelLimitsCache[tenantId]
	modelLimitsMu.RUnlock()
	if ok && time.Since(e.at) < modelLimitsCacheTTL {
		return e.byKey
	}
	byKey, err := fetchModelTokenLimits(tenantId)
	if err != nil {
		// Transient failure: serve the stale entry if one exists, retry next call.
		slog.Error("model-limits: catalog fetch failed", "error", err, "tenant_id", tenantId)
		if ok {
			return e.byKey
		}
		return nil
	}
	modelLimitsMu.Lock()
	// Opportunistic eviction: drop entries for tenants that stopped calling
	// long ago, so the map stays bounded by ACTIVE tenants rather than every
	// tenant ever seen. Piggybacked on the (rare) write path — no ticker.
	for k, stale := range modelLimitsCache {
		if time.Since(stale.at) > 4*modelLimitsCacheTTL {
			delete(modelLimitsCache, k)
		}
	}
	modelLimitsCache[tenantId] = modelLimitsEntry{byKey: byKey, at: time.Now()}
	modelLimitsMu.Unlock()
	return byKey
}

var (
	// "…-v1" / "-v2" invocation suffix left after cutting a Bedrock ":0" tail.
	trailingInvocationVersionRE = regexp.MustCompile(`-v\d+$`)
	// "-20250929"-style date suffix on point-release ids.
	trailingDateRE = regexp.MustCompile(`-\d{8}$`)
	// digit-hyphen-digit → digit-dot-digit, so Bedrock's "claude-sonnet-4-6"
	// meets the catalog's "claude-sonnet-4.6".
	versionSeparatorRE = regexp.MustCompile(`(\d)-(\d)`)
)

// canonicalModelID reduces a provider-qualified model id to the bare dotted
// form the built-in catalog rows use. Bedrock cross-region ids stack several
// decorations the fixed-list normalizeModel cannot remove — a region segment
// ("us." / "eu."), the vendor segment, an invocation suffix ("-v1:0") and
// hyphenated version numbers — and any one of them alone was enough to miss
// the catalog and land the model back on the 4096 floor (#36449's original
// customer id, "us.anthropic.claude-sonnet-4-6"). Applied to BOTH catalog keys
// and lookup probes, so both sides meet in the same space regardless of which
// convention a row or caller uses.
func canonicalModelID(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, ':'); i >= 0 {
		m = m[:i]
	}
	for _, p := range []string{"us.", "eu.", "apac.", "jp.", "au.", "ca.", "global."} {
		if strings.HasPrefix(m, p) {
			m = strings.TrimPrefix(m, p)
			break
		}
	}
	for _, p := range []string{"anthropic.", "amazon.", "meta.", "google.", "vertex.", "vertex/", "openai.", "azure.", "mistral.", "cohere.", "ai21.", "models/"} {
		if strings.HasPrefix(m, p) {
			m = strings.TrimPrefix(m, p)
			break
		}
	}
	m = trailingInvocationVersionRE.ReplaceAllString(m, "")
	m = versionSeparatorRE.ReplaceAllString(m, "$1.$2")
	return m
}

// lookupModelTokenLimits finds a catalog row for (provider, model): exact
// provider:model first, then the model under any provider, repeated for the
// legacy-normalized, canonical and date-stripped-canonical forms of the id.
// Model-only matches pick the lexicographically smallest provider so repeat
// lookups are deterministic.
func lookupModelTokenLimits(accountId, provider, model string) (modelTokenLimits, bool) {
	tenantId := ""
	if accountId != "" {
		tenantId = TenantForPricing(accountId)
	}
	catalog := loadModelLimitsCatalog(tenantId)
	if len(catalog) == 0 {
		return modelTokenLimits{}, false
	}
	prov := strings.ToLower(strings.TrimSpace(provider))
	trimmed := strings.ToLower(strings.TrimSpace(model))
	probes := []string{trimmed}
	addProbe := func(p string) {
		for _, existing := range probes {
			if existing == p {
				return
			}
		}
		probes = append(probes, p)
	}
	addProbe(strings.ToLower(normalizeModel(model)))
	canonical := canonicalModelID(model)
	addProbe(canonical)
	// A dated point release ("claude-sonnet-4.6-20260115") falls back to its
	// family row when no dated row exists.
	addProbe(trailingDateRE.ReplaceAllString(canonical, ""))
	for _, m := range probes {
		if m == "" {
			continue
		}
		if prov != "" {
			if l, ok := catalog[prov+":"+m]; ok {
				return l, true
			}
		}
		// Split at the FIRST colon only: Bedrock model ids themselves contain
		// colons ("...-v1:0"), so a suffix test would let a partial model id
		// match the tail of a longer one.
		best := ""
		for k := range catalog {
			if _, modelPart, found := strings.Cut(k, ":"); found && modelPart == m && (best == "" || k < best) {
				best = k
			}
		}
		if best != "" {
			return catalog[best], true
		}
	}
	return modelTokenLimits{}, false
}

// ResolveMaxOutputTokens returns the output-token ceiling for a model, or 0
// when nothing is configured anywhere (caller applies its floor + WARN).
// DefaultMaxOutputTokensFloor is applied when neither the per-account config key
// nor the pricing catalog carries a max-output value. It is a floor, not a
// per-model table: raising it does not reintroduce the code-based limits V878
// removed (#36471), it only changes what an uncatalogued model gets.
//
// 4096 was chosen when the catalog did not exist and every model relied on it;
// with the catalog covering the known models, the remainder are self-hosted
// (Qwen, Nemotron, Gemma) whose real ceilings are far above 4096, and the low
// floor cost them a continuation loop on every long response. 16384 matches the
// lowest ceiling among catalogued chat models (gpt-4o).
const DefaultMaxOutputTokensFloor = 16384

func ResolveMaxOutputTokens(accountId, provider, model string) int {
	if v := GetLLMModelIntConfig(accountId, provider, model, "llm_model_max_output_tokens", 0); v > 0 {
		return v
	}
	if l, ok := lookupModelTokenLimits(accountId, provider, model); ok && l.MaxOutput > 0 {
		return l.MaxOutput
	}
	return 0
}
