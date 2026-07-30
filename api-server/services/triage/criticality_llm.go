package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/services/llm"
	"nudgebee/services/security"
)

// llmWorkload is one workload handed to the batch criticality classifier. It carries the workload's
// identity (the strongest cue for what it IS) plus the deterministic topology hints, so the model
// classifies by business meaning rather than structure alone.
type llmWorkload struct {
	CloudResourceID string
	Name            string
	Namespace       string
	Kind            string
	Image           string
	AppLabel        string
	CustomerFacing  bool
	GraphKnown      bool
	FanIn           int
}

// llmCriticalityVerdict is the model's per-workload judgement.
type llmCriticalityVerdict struct {
	Criticality string
	Reason      string
}

// llmClassifyBatchSize bounds how many workloads go in one prompt. ~40 keeps the query well under the
// llm-server limit while amortizing the call across the account's inventory.
const llmClassifyBatchSize = 40

// classifyWorkloadsLLM asks the LLM to tier a batch of workloads by business criticality. It reuses
// the generic @llm agent via llm.ChatCompletion (the same path as the signal-class classifier), so
// the account's global-context ("payments and checkout are business-critical; sandbox-* is throwaway")
// is auto-injected as <global_preferences> and steers the result. Returns a map keyed by
// cloud_resource_id; classes the model omits or mis-formats are simply absent (caller falls back).
func classifyWorkloadsLLM(ctx context.Context, tenant, account string, items []llmWorkload) (map[string]llmCriticalityVerdict, error) {
	out := make(map[string]llmCriticalityVerdict, len(items))
	if tenant == "" || account == "" || len(items) == 0 {
		return out, nil
	}
	sc := security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil)

	for start := 0; start < len(items); start += llmClassifyBatchSize {
		end := start + llmClassifyBatchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[start:end]
		resp, err := llm.ChatCompletion(sc, llm.ConversationApiRequest{
			Query:     criticalityClassifyPrompt(batch),
			AccountId: account,
			UserId:    systemUserID,
			Async:     false,
			Source:    "workload_criticality_classification",
		})
		if err != nil {
			return out, err // let the caller fall back to deterministic for the whole sweep
		}
		if resp == nil || len(resp.Response) == 0 {
			continue
		}
		parseCriticalityVerdicts(resp.Response[0], batch, out)
	}
	return out, nil
}

// criticalityClassifyPrompt builds the batch classifier query. All guidance is inline (@llm has no
// domain system prompt). Names/images are called out as the strongest cue so the model tiers by what
// a workload IS, not merely whether it is exposed.
func criticalityClassifyPrompt(batch []llmWorkload) string {
	var b strings.Builder
	b.WriteString("@llm You are a Kubernetes workload BUSINESS-CRITICALITY reviewer. The workloads below were flagged as potentially important by a topology heuristic (they are network-exposed or have many dependents). Your job is PRECISION: confirm the genuinely important ones and DEMOTE the false positives the heuristic can't recognize — non-production, demo/test/e2e/sandbox, benchmarks, documentation, and one-off tooling are NOT business-critical even when exposed. Judge by what each workload IS (name/image/labels/namespace), not merely that it is exposed. Do NOT investigate or call tools.\n\n")
	b.WriteString("## Tiers\n")
	b.WriteString("critical = genuinely business/customer-critical: payment, checkout, order, auth/identity (e.g. keycloak, dex), the primary user-facing API/gateway, or a primary production database/datastore whose loss causes a customer-visible outage or data loss.\n")
	b.WriteString("high = important shared/internal services: core internal APIs, shared datastores/queues/caches, ingress controllers/gateways, and services that many others depend on.\n")
	b.WriteString("medium = standard application services with no special criticality. This is the DEFAULT — use it when unsure.\n")
	b.WriteString("low = non-production or non-business: dev/test/demo/e2e, benchmarks, documentation, one-off tooling/jobs, or monitoring/observability components.\n\n")
	b.WriteString("## Signals per workload (hints — names/images still dominate)\n")
	b.WriteString("customer_facing=true means ingress/LB-backed (a request path). fan_in=N means N other services are observed to depend on it. Absence of a hint is not evidence of low importance.\n\n")
	b.WriteString("## Workloads\n")
	for i, w := range batch {
		fmt.Fprintf(&b, "%d. name=%q namespace=%q kind=%s", i+1, w.Name, w.Namespace, w.Kind)
		if w.Image != "" {
			fmt.Fprintf(&b, " image=%q", w.Image)
		}
		if w.AppLabel != "" {
			fmt.Fprintf(&b, " app=%q", w.AppLabel)
		}
		if w.GraphKnown {
			fmt.Fprintf(&b, " customer_facing=%t fan_in=%d", w.CustomerFacing, w.FanIn)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Output\nReturn ONLY a JSON array — one object per workload, in order, for EVERY index — EXACTLY:\n")
	b.WriteString(`[{"i":1,"criticality":"critical|high|medium|low","reason":"<=100 chars"}]`)
	b.WriteString("\nNo markdown fence, no prose.")
	return b.String()
}

// parseCriticalityVerdicts extracts the JSON array from the model text and maps each entry back to its
// workload by 1-based index, writing valid verdicts into out (keyed by cloud_resource_id).
func parseCriticalityVerdicts(text string, batch []llmWorkload, out map[string]llmCriticalityVerdict) {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j >= i {
			s = s[i : j+1]
		}
	}
	var rows []struct {
		I           int    `json:"i"`
		Criticality string `json:"criticality"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		return
	}
	for _, r := range rows {
		idx := r.I - 1
		if idx < 0 || idx >= len(batch) {
			continue
		}
		lvl := strings.ToLower(strings.TrimSpace(r.Criticality))
		if !isValidCriticality(lvl) {
			continue
		}
		out[batch[idx].CloudResourceID] = llmCriticalityVerdict{Criticality: lvl, Reason: strings.TrimSpace(r.Reason)}
	}
}
