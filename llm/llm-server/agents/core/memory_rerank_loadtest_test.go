//go:build e2e

package core

import (
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// TestMemoryRerankLoad_E2E is a latency load test for the memory reranker — the
// extra LLM step this change adds to memory Compose. It reports numbers a
// reviewer will ask for:
//
//   - SEQUENTIAL per-call percentiles (warm steady-state): the cost of ONE
//     rerank LLM call.
//   - CONCURRENT round wall-clock: C reranks fired simultaneously per round.
//     Because Compose runs its layers in parallel, this round wall-clock is the
//     worst-case latency this change adds to a SINGLE request when C layers
//     rerank at once (patterns + decisions + collective + preferences => C=4).
//
// A one-time warmup discards the cold config-resolution + model-init (~1.5s on
// the very first call in a fresh process), which is not a per-turn cost.
//
//	Run: set -a; source .env; set +a; \
//	  TEST_TENANT=<t> TEST_ACCOUNT=<a> TEST_USER=<u> \
//	  [RERANK_LOAD_N=30 RERANK_LOAD_CONC=4 RERANK_LOAD_ROUNDS=10] \
//	  go test -tags e2e -run TestMemoryRerankLoad_E2E -v -timeout 600s ./agents/core/
func TestMemoryRerankLoad_E2E(t *testing.T) {
	if GetConversationDao() == nil {
		t.Skip("db not available")
	}
	RequireEnv(t, "TEST_TENANT", "TEST_ACCOUNT", "TEST_USER")

	tenant := os.Getenv("TEST_TENANT")
	account := os.Getenv("TEST_ACCOUNT")
	user := os.Getenv("TEST_USER")

	seqN := envInt("RERANK_LOAD_N", 30)
	conc := envInt("RERANK_LOAD_CONC", 4)
	rounds := envInt("RERANK_LOAD_ROUNDS", 10)

	ctx := security.NewRequestContextForTenantAccountAdmin(tenant, user, []string{account})

	orig := config.Config.MemoryRerankEnabled
	config.Config.MemoryRerankEnabled = true
	defer func() { config.Config.MemoryRerankEnabled = orig }()

	query := "can you identify these failures, why it's happening — postgres slow queries on the primary db " +
		"in the dev namespace; more than 5 queries exceeding 10s execution time; check pg_stat_activity and Query Insights"

	results := toolcore.QueryRAGCollection(
		ctx.GetContext(), user, tenant, tenant, query, "memory_patterns", "",
		12, "", "", "", false,
	)
	if len(results) < 2 {
		t.Skipf("need >=2 memory_patterns candidates, got %d (is rag-server up / tenant seeded?)", len(results))
	}
	docs := make([]string, len(results))
	for i, r := range results {
		docs[i] = r.Document
	}
	goCtx := withMemoryRerankIdentity(ctx, account, user, "", "")

	call := func() (time.Duration, bool) {
		start := time.Now()
		_, ok := llmRerankMemories(goCtx, query, docs)
		return time.Since(start), ok
	}

	t.Logf("config: candidates=%d seqN=%d concurrency=%d rounds=%d", len(docs), seqN, conc, rounds)

	// Warmup — discard the one-time cold config resolution / model init.
	for i := 0; i < 2; i++ {
		call()
	}

	// --- Sequential steady-state: per-call latency distribution ---
	seq := make([]time.Duration, 0, seqN)
	var seqFail int
	for i := 0; i < seqN; i++ {
		dt, ok := call()
		if !ok {
			seqFail++
			continue
		}
		seq = append(seq, dt)
	}
	sort.Slice(seq, func(a, b int) bool { return seq[a] < seq[b] })
	t.Logf("SEQUENTIAL per-call (n=%d, fail=%d): min=%s p50=%s p90=%s p99=%s max=%s mean=%s",
		len(seq), seqFail,
		round(seq[0]), round(pctile(seq, 50)), round(pctile(seq, 90)),
		round(pctile(seq, 99)), round(seq[len(seq)-1]), round(mean(seq)))

	// --- Concurrent burst: C reranks in parallel per round ---
	// Per-round wall-clock = latency added to one request when C layers rerank
	// together (Compose joins them with a single wg.Wait).
	perCall := make([]time.Duration, 0, conc*rounds)
	roundWall := make([]time.Duration, 0, rounds)
	var concFail int64
	var mu sync.Mutex
	for r := 0; r < rounds; r++ {
		var wg sync.WaitGroup
		roundStart := time.Now()
		for w := 0; w < conc; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				dt, ok := call()
				if !ok {
					atomic.AddInt64(&concFail, 1)
				}
				mu.Lock()
				perCall = append(perCall, dt)
				mu.Unlock()
			}()
		}
		wg.Wait()
		roundWall = append(roundWall, time.Since(roundStart))
	}
	sort.Slice(perCall, func(a, b int) bool { return perCall[a] < perCall[b] })
	sort.Slice(roundWall, func(a, b int) bool { return roundWall[a] < roundWall[b] })

	t.Logf("CONCURRENT per-call (C=%d, n=%d, fail=%d): p50=%s p90=%s p99=%s max=%s",
		conc, len(perCall), concFail,
		round(pctile(perCall, 50)), round(pctile(perCall, 90)), round(pctile(perCall, 99)), round(perCall[len(perCall)-1]))
	t.Logf("CONCURRENT round wall-clock (C=%d parallel = worst-case Compose add, n=%d): p50=%s p90=%s max=%s",
		conc, len(roundWall),
		round(pctile(roundWall, 50)), round(pctile(roundWall, 90)), round(roundWall[len(roundWall)-1]))
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func round(d time.Duration) time.Duration { return d.Round(time.Millisecond) }

// pctile returns the p-th percentile (nearest-rank) of an ascending-sorted slice.
func pctile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int((p/100)*float64(len(sorted)) + 0.5)
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func mean(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	return sum / time.Duration(len(ds))
}
