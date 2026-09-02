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

// classifyWorkloads is the criticality classifier the sweep calls. It is a variable purely so tests
// can exercise the review-failed branch (which must HOLD an account's existing rows rather than
// rewrite them from the recall stage alone) without needing a reachable llm-server.
var classifyWorkloads = classifyWorkloadsLLM

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
		parseCriticalityVerdicts(ctx, resp.Response[0], batch, out)
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
	b.WriteString("medium = you cannot tell from the information given. Every workload here already carries a measured topology signal, so medium means ONLY 'no opinion' — it leaves that measured signal in place. Never use medium to say a workload is unimportant.\n")
	b.WriteString("low = you are ACTIVELY judging this workload as non-production or non-business: dev/test/demo/e2e, benchmarks, documentation, one-off tooling/jobs, or monitoring/observability components. This is how you demote a false positive — medium will not demote it.\n\n")
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
	b.WriteString(`[{"i":1,"name":"<the workload's name, copied exactly>","criticality":"critical|high|medium|low","reason":"<=100 chars"}]`)
	b.WriteString("\nThe `name` must be copied verbatim from the workload at index `i`; it is checked, and a mismatch discards that entry.")
	b.WriteString("\nNo markdown fence, no prose.")
	return b.String()
}

// parseCriticalityVerdicts extracts the JSON array from the model text and maps each entry back to its
// workload by 1-based index, writing valid verdicts into out (keyed by cloud_resource_id).
//
// The index alone is not trusted. Batches are numbered 1..N independently, and a model that continues
// numbering across batches (41..80) or returns a short list would otherwise land verdicts on the WRONG
// workload — a silent mis-tiering that looks like a successful review. Each entry therefore echoes the
// workload name back and is discarded unless it matches the workload at that index. An entry that is
// dropped simply leaves its workload on the deterministic verdict, which is the safe direction.
func parseCriticalityVerdicts(ctx context.Context, text string, batch []llmWorkload, out map[string]llmCriticalityVerdict) {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j >= i {
			s = s[i : j+1]
		}
	}
	var rows []struct {
		I           int    `json:"i"`
		Name        string `json:"name"`
		Criticality string `json:"criticality"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		slog.WarnContext(ctx, "criticality classifier returned unparseable JSON; batch left on deterministic verdicts",
			"batch_size", len(batch), "error", err)
		return
	}
	mismatched := 0
	for _, r := range rows {
		idx := r.I - 1
		if idx < 0 || idx >= len(batch) {
			mismatched++
			continue
		}
		// An omitted name is tolerated (older/terser model output); a WRONG one is not. The match is
		// deliberately forgiving about case and surrounding space: k8s object names are already
		// lowercase by RFC 1123, so two workloads in a batch can never differ only in case, and being
		// strict there would only drop verdicts a model title-cased on the way out.
		if name := strings.TrimSpace(r.Name); name != "" && !strings.EqualFold(name, strings.TrimSpace(batch[idx].Name)) {
			mismatched++
			continue
		}
		lvl := strings.ToLower(strings.TrimSpace(r.Criticality))
		if !isValidCriticality(lvl) {
			continue
		}
		out[batch[idx].CloudResourceID] = llmCriticalityVerdict{Criticality: lvl, Reason: strings.TrimSpace(r.Reason)}
	}
	if mismatched > 0 {
		slog.WarnContext(ctx, "criticality classifier returned entries that did not match their workload; dropped",
			"dropped", mismatched, "batch_size", len(batch), "returned", len(rows))
	}
}
