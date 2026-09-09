package core

import (
	"context"
	"encoding/json"
	"log/slog"

	"nudgebee/services/internal/database"
)

// UncertainClassificationCandidate is the source- and kind-agnostic shape
// recorded when a classifier/matcher tried every rule it knows and still
// isn't confident enough to decide — but saw SOME signal along the way worth
// keeping for later review/backtracking. One row per (TenantID, Source,
// ClassificationKind, CandidateName, CandidateNamespace, CandidateCluster);
// CandidateCluster is part of that identity because the same
// namespace/service name in two clusters is two independent near-misses to
// investigate, not one row whose cluster is overwritten by whichever
// cluster the rebuild processed last. Repeat sightings
// overwrite reason/evidence with the latest occurrence and bump
// occurrence_count.
type UncertainClassificationCandidate struct {
	TenantID           string
	Source             string // e.g. "traces"; future: "ebpf", "aws", "gcp", "azure", "k8s"
	ClassificationKind string // "node_type" | "specific_type" | "node_match" (future)
	CandidateType      string // the type/value that was rejected or unmapped
	CandidateName      string
	CandidateNamespace string
	CandidateCluster   string
	ReasonCode         string // e.g. "outbound_span_kind", "no_specific_type_mapping"
	ReasonDescription  string
	Evidence           map[string]interface{}
}

// RecordUncertainClassification best-effort upserts a near-miss classification
// signal for later review. Fire-and-forget by construction: it returns
// nothing, so no caller can accidentally let a failure here affect graph-build
// success. Pass a short-timeout context derived from context.Background(),
// NOT the build's request context.
func RecordUncertainClassification(ctx context.Context, dbManager *database.DatabaseManager, c UncertainClassificationCandidate) {
	if dbManager == nil || dbManager.Db == nil ||
		c.TenantID == "" || c.Source == "" || c.ClassificationKind == "" ||
		c.CandidateName == "" || c.ReasonCode == "" {
		return
	}
	// A nil map marshals to `null`, which satisfies the column's NOT NULL but
	// stores jsonb null rather than the empty object the column default
	// advertises — so `evidence ->> 'k'` behaves differently on those rows.
	if c.Evidence == nil {
		c.Evidence = map[string]interface{}{}
	}
	evidenceJSON, err := json.Marshal(c.Evidence)
	if err != nil {
		slog.Warn("failed to marshal uncertain-classification evidence", "error", err)
		return
	}
	_, err = dbManager.Db.ExecContext(ctx, `
		INSERT INTO public.knowledge_graph_classification_review
			(tenant_id, source, classification_kind, candidate_type, candidate_name,
			 candidate_namespace, candidate_cluster, reason_code, reason_description,
			 evidence, occurrence_count, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 1, now(), now())
		ON CONFLICT (tenant_id, source, classification_kind, candidate_name,
		             candidate_namespace, candidate_cluster)
		DO UPDATE SET
			candidate_type      = EXCLUDED.candidate_type,
			reason_code         = EXCLUDED.reason_code,
			reason_description  = EXCLUDED.reason_description,
			evidence            = EXCLUDED.evidence,
			occurrence_count    = knowledge_graph_classification_review.occurrence_count + 1,
			last_seen_at        = now(),
			updated_at          = now()
	`, c.TenantID, c.Source, c.ClassificationKind, c.CandidateType, c.CandidateName,
		c.CandidateNamespace, c.CandidateCluster, c.ReasonCode, c.ReasonDescription, string(evidenceJSON))
	if err != nil {
		slog.Warn("failed to record uncertain classification",
			"source", c.Source, "kind", c.ClassificationKind, "candidate_name", c.CandidateName,
			"reason_code", c.ReasonCode, "error", err)
	}
}
