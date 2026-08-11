package metering

import (
	"log/slog"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
)

var (
	// trailing provider date suffix — Anthropic/Gemini "-20251001" (contiguous) OR
	// OpenAI "-2024-07-18" (dashed). Either form is stripped to the undated name.
	dateSuffixRe = regexp.MustCompile(`-\d{8}$|-\d{4}-\d{2}-\d{2}$`)
	// a dashed 1-digit.1-digit version segment, e.g. "-4-5-" in claude-haiku-4-5-…
	// (word-bounded so a date like "-4-20250514" is NOT touched).
	dashVersionRe = regexp.MustCompile(`\b(\d)-(\d)\b`)
)

// normalizedCandidates returns fallback catalog keys to try when an exact model
// match misses. Providers send real IDs (dated, dash-versioned like
// "claude-haiku-4-5-20251001") while the NB catalog stores hand-normalized names
// (dotted version, sometimes undated — "claude-sonnet-4.5-20250929"). Order:
// date-stripped, dash→dot version, and both.
func normalizedCandidates(model string) []string {
	var out []string
	add := func(s string) {
		if s == "" || s == model || slices.Contains(out, s) {
			return
		}
		out = append(out, s)
	}
	noDate := dateSuffixRe.ReplaceAllString(model, "")
	add(noDate)
	add(dashVersionRe.ReplaceAllString(model, "$1.$2"))
	add(dashVersionRe.ReplaceAllString(noDate, "$1.$2"))
	return out
}

// modelPrice holds per-million-token rates for a model (standard tier). Long-
// context tiers are omitted from the cost estimate for now — the estimate errs
// conservative (never under-charges).
type modelPrice struct {
	Input         float64 `db:"cost_per_million_input_tokens"`
	Output        float64 `db:"cost_per_million_output_tokens"`
	CachedInput   float64 `db:"cost_per_million_cached_input_tokens"`
	CacheCreation float64 `db:"cost_per_million_cache_creation_tokens"`
	Model         string  `db:"model_name"`
	// TenantID is "" for a built-in/global row (tenant_id IS NULL) and a tenant uuid for a
	// per-tenant override. llm_model_pricing gained tenant scoping (V861), so a single
	// model_name can now carry BOTH a built-in and per-tenant override rows.
	TenantID string `db:"tenant_id"`
}

// catalog is the loaded pricing snapshot, split so a tenant's override never bleeds onto
// another tenant. A built-in (global) price is keyed by model_name; a tenant override is
// keyed by tenant_id then model_name.
type catalog struct {
	builtin  map[string]modelPrice            // model_name → built-in price (tenant_id IS NULL)
	byTenant map[string]map[string]modelPrice // tenant_id → model_name → override price
}

// resolve looks up model (with normalized-name fallbacks) in a single price map.
func resolve(m map[string]modelPrice, model string) (modelPrice, bool) {
	if mp, ok := m[model]; ok {
		return mp, true
	}
	// Providers send real IDs (dated / dash-versioned); the catalog stores normalized names.
	for _, cand := range normalizedCandidates(model) {
		if mp, ok := m[cand]; ok {
			return mp, true
		}
	}
	return modelPrice{}, false
}

// lookup resolves the price for (tenant, model): the tenant's own override wins, else the
// built-in (mirrors llm-server's ListModelPricing precedence — tenant, then global).
func (c *catalog) lookup(tenantID, model string) (modelPrice, bool) {
	if tenantID != "" {
		if tm := c.byTenant[tenantID]; tm != nil {
			if mp, ok := resolve(tm, model); ok {
				return mp, true
			}
		}
	}
	return resolve(c.builtin, model)
}

// Pricer computes request cost (USD) from the llm_model_pricing catalog, cached
// and refreshed. Unknown models cost 0 (can't estimate) and are logged once.
type Pricer struct {
	cur  atomic.Pointer[catalog]
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Map // models already warned about
}

// NewPricer loads the pricing catalog and refreshes it periodically. A load error
// yields an empty catalog (costs 0) rather than failing startup.
func NewPricer(refresh time.Duration) *Pricer {
	p := &Pricer{done: make(chan struct{})}
	p.rebuild()
	if refresh <= 0 {
		refresh = 5 * time.Minute
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		t := time.NewTicker(refresh)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				p.rebuild()
			case <-p.done:
				return
			}
		}
	}()
	return p
}

func (p *Pricer) rebuild() {
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("pricing: metastore unavailable", "error", err)
		if p.cur.Load() == nil {
			p.cur.Store(&catalog{builtin: map[string]modelPrice{}, byTenant: map[string]map[string]modelPrice{}})
		}
		return
	}
	var rows []modelPrice
	if err := db.QueryAndScan(&rows, `SELECT model_name,
		cost_per_million_input_tokens, cost_per_million_output_tokens,
		COALESCE(cost_per_million_cached_input_tokens, 0)   AS cost_per_million_cached_input_tokens,
		COALESCE(cost_per_million_cache_creation_tokens, 0) AS cost_per_million_cache_creation_tokens,
		COALESCE(tenant_id::text, '')                       AS tenant_id
		FROM llm_model_pricing`); err != nil {
		slog.Error("pricing: load failed", "error", err)
		return
	}
	c := &catalog{builtin: map[string]modelPrice{}, byTenant: map[string]map[string]modelPrice{}}
	var tenantRows int
	for _, r := range rows {
		if r.TenantID == "" {
			c.builtin[r.Model] = r
			continue
		}
		tm := c.byTenant[r.TenantID]
		if tm == nil {
			tm = map[string]modelPrice{}
			c.byTenant[r.TenantID] = tm
		}
		tm[r.Model] = r
		tenantRows++
	}
	p.cur.Store(c)
	slog.Info("pricing: loaded catalog", "builtin_models", len(c.builtin), "tenant_override_rows", tenantRows, "tenants", len(c.byTenant))
}

// CostUSD estimates the cost of one request for a given tenant. A tenant's own price override
// wins over the built-in/global rate; an empty tenantID resolves against built-ins only.
// Returns 0 for an unknown model.
func (p *Pricer) CostUSD(tenantID, model string, input, output, cacheRead, cacheWrite int) float64 {
	cat := p.cur.Load()
	if cat == nil {
		return 0
	}
	mp, ok := cat.lookup(tenantID, model)
	if !ok {
		if _, warned := p.once.LoadOrStore(model, true); !warned {
			slog.Warn("pricing: no catalog entry — cost counted as 0", "model", model)
		}
		return 0
	}
	// Conservative: input at full rate + cache read/write at their rates + output.
	c := float64(input)*mp.Input +
		float64(output)*mp.Output +
		float64(cacheRead)*mp.CachedInput +
		float64(cacheWrite)*mp.CacheCreation
	return c / 1_000_000
}

func (p *Pricer) Close() error {
	close(p.done)
	p.wg.Wait()
	return nil
}
