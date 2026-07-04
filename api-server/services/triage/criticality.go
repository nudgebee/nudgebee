package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Workload criticality levels (named enum, stored as text in workload_criticality.criticality).
const (
	CriticalityCritical = "critical"
	CriticalityHigh     = "high"
	CriticalityMedium   = "medium"
	CriticalityLow      = "low"
)

// Provenance of a criticality row. 'user' is authoritative and sticky — the discovery/derive
// path never overwrites it.
const (
	CriticalitySourceUser    = "user"
	CriticalitySourceFact    = "fact_signal"
	CriticalitySourceLLM     = "llm_inferred"
	CriticalityStatusActive  = "active"
	CriticalityStatusPropose = "proposed"
)

// fanInHighThreshold is the number of observed dependents at/above which a workload is treated as a
// genuinely shared dependency (→ criticality high). Kept deliberately high: in a trace-instrumented
// cluster a low bar (e.g. 3) tags ~20% of workloads "high" and the tier stops being meaningful, so we
// reserve "high" for real hubs (a shared datastore/queue/gateway with many dependents). Under-tiering
// to the medium default is safe — operators raise anything we miss via the review screen.
const fanInHighThreshold = 10

// WorkloadCriticality mirrors a row of the workload_criticality table.
type WorkloadCriticality struct {
	Criticality    string  `db:"criticality"`
	Source         string  `db:"source"`
	Confidence     float64 `db:"confidence"`
	Rationale      string  `db:"rationale"`
	IsUserOverride bool    `db:"is_user_override"`
}

// deriveCriticalityFromFacts turns observed workload facts into a criticality level. It only returns
// ok=true when a real signal exists — a workload with no distinguishing signal is left untiered
// (medium is the implicit default in scoring) rather than persisting a noise row for every workload.
//
// Auto-derivation is deliberately capped at `high`: `critical` is the top business tier and is
// reserved for an explicit operator decision (a review-screen override, or a workload the operator
// labelled tier=critical themselves). Ingress/LB-backing and a large fan-in are strong "this matters"
// signals, but most ingress-backed workloads are internal APIs, not truly customer-critical — so we
// surface them as `high` for the operator to promote, rather than auto-inflating `critical`.
// Precedence: operator-declared label > customer-facing (ingress/LB) > high dependency fan-in.
func deriveCriticalityFromFacts(f workloadFacts) (level string, confidence float64, rationale string, ok bool) {
	if !f.found {
		return "", 0, "", false
	}
	if f.declaredTier != "" {
		if lvl := normalizeDeclaredTier(f.declaredTier); lvl != "" {
			return lvl, 0.95, "operator-declared label " + f.declaredTier, true
		}
	}
	if f.graphKnown && f.customerFacing {
		return CriticalityHigh, 0.9, "ingress/LB-backed (customer-facing request path)", true
	}
	if f.graphKnown && f.fanIn >= fanInHighThreshold {
		return CriticalityHigh, 0.8, fmt.Sprintf("%d services depend on it (shared dependency)", f.fanIn), true
	}
	return "", 0, "", false
}

// normalizeDeclaredTier maps a "key=value" operator label to a criticality level. Conservative: it
// only recognizes unambiguous words/tiers and returns "" for anything it can't confidently classify
// (numeric-only tiers are too ambiguous to trust), so an unrecognized label falls through to facts.
func normalizeDeclaredTier(kv string) string {
	v := kv
	if i := strings.Index(kv, "="); i >= 0 {
		v = kv[i+1:]
	}
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.Contains(v, "critical") || v == "tier-0" || v == "tier0" || v == "p0" || v == "gold":
		return CriticalityCritical
	case v == "high" || v == "tier-1" || v == "tier1" || v == "p1" || v == "silver":
		return CriticalityHigh
	case v == "medium" || v == "tier-2" || v == "tier2" || v == "p2" || v == "bronze":
		return CriticalityMedium
	case v == "low" || v == "tier-3" || v == "tier3" || v == "p3":
		return CriticalityLow
	}
	return ""
}

// resolveWorkloadCriticality reads the persisted criticality for a workload by its resource id.
// Returns ok=false when the workload isn't tiered. The id is parsed to a real uuid so the lookup
// hits the unique index (a ::text cast would force a sequential scan).
func resolveWorkloadCriticality(ctx context.Context, db *sqlx.DB, account, cloudResourceID string) (*WorkloadCriticality, bool) {
	if db == nil || account == "" || cloudResourceID == "" {
		return nil, false
	}
	resourceID, err := uuid.Parse(cloudResourceID)
	if err != nil {
		return nil, false
	}
	var wc WorkloadCriticality
	err = db.GetContext(ctx, &wc, `
		SELECT criticality, source, COALESCE(confidence, 0) AS confidence,
		       COALESCE(rationale, '') AS rationale, is_user_override
		FROM workload_criticality
		WHERE cloud_account_id = $1 AND status = 'active' AND cloud_resource_id = $2
		LIMIT 1`, account, resourceID)
	if err != nil {
		return nil, false
	}
	return &wc, true
}

// upsertAutoCriticality persists an auto-classified criticality for a workload (source is
// 'llm_inferred' for an LLM verdict or 'fact_signal' for the deterministic fallback). It NEVER
// overwrites a user override (the WHERE guard on the DO UPDATE), so operator curation is sticky
// across re-runs.
func upsertAutoCriticality(ctx context.Context, db *sqlx.DB, tenant, account, cloudResourceID, namespace, source, level, rationale string, confidence float64, signals map[string]interface{}) {
	if db == nil || tenant == "" || account == "" || cloudResourceID == "" || level == "" {
		return
	}
	signalsJSON, _ := json.Marshal(signals)
	_, err := db.ExecContext(ctx, `
		INSERT INTO workload_criticality
		    (tenant_id, cloud_account_id, cloud_resource_id, namespace,
		     criticality, source, confidence, signals, rationale, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'active')
		ON CONFLICT (cloud_account_id, cloud_resource_id)
		DO UPDATE SET
		    criticality = EXCLUDED.criticality,
		    source      = EXCLUDED.source,
		    confidence  = EXCLUDED.confidence,
		    signals     = EXCLUDED.signals,
		    rationale   = EXCLUDED.rationale,
		    namespace   = EXCLUDED.namespace,
		    updated_at  = now()
		WHERE workload_criticality.is_user_override = false
		  AND workload_criticality.source <> 'user'`,
		tenant, account, cloudResourceID, namespace, level, source, confidence, string(signalsJSON), rationale)
	if err != nil {
		slog.WarnContext(ctx, "failed to upsert auto workload criticality",
			"cloud_resource_id", cloudResourceID, "source", source, "error", err)
	}
}
