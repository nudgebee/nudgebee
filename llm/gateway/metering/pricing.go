package metering

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"nudgebee/llm-gateway/common"
)

// modelPrice holds per-million-token rates for a model (standard tier). Long-
// context tiers are omitted from the budget ESTIMATE for now — the estimate errs
// conservative (never under-charges), which is the safe side for budget guardrails.
type modelPrice struct {
	Input         float64 `db:"cost_per_million_input_tokens"`
	Output        float64 `db:"cost_per_million_output_tokens"`
	CachedInput   float64 `db:"cost_per_million_cached_input_tokens"`
	CacheCreation float64 `db:"cost_per_million_cache_creation_tokens"`
	Model         string  `db:"model_name"`
}

// Pricer computes request cost (USD) from the llm_model_pricing catalog, cached
// and refreshed. Used to accumulate cost budgets; unknown models cost 0 (can't
// estimate) and are logged once.
type Pricer struct {
	cur  atomic.Pointer[map[string]modelPrice]
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
			m := map[string]modelPrice{}
			p.cur.Store(&m)
		}
		return
	}
	var rows []modelPrice
	if err := db.QueryAndScan(&rows, `SELECT model_name,
		cost_per_million_input_tokens, cost_per_million_output_tokens,
		COALESCE(cost_per_million_cached_input_tokens, 0)   AS cost_per_million_cached_input_tokens,
		COALESCE(cost_per_million_cache_creation_tokens, 0) AS cost_per_million_cache_creation_tokens
		FROM llm_model_pricing`); err != nil {
		slog.Error("pricing: load failed", "error", err)
		return
	}
	m := make(map[string]modelPrice, len(rows))
	for _, r := range rows {
		m[r.Model] = r
	}
	p.cur.Store(&m)
	slog.Info("pricing: loaded catalog", "models", len(m))
}

// CostUSD estimates the cost of one request. Returns 0 for an unknown model.
func (p *Pricer) CostUSD(model string, input, output, cacheRead, cacheWrite int) float64 {
	cat := p.cur.Load()
	if cat == nil {
		return 0
	}
	mp, ok := (*cat)[model]
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
