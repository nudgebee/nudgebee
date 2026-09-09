package core

// DB-gated test for RecordUncertainClassification's upsert semantics — the one
// piece of genuinely new SQL behavior (increment + latest-wins overwrite +
// preserved first_seen_at + kind-scoped uniqueness) that a mocked unit test
// can't meaningfully verify. Skips cleanly when no Postgres is reachable.

import (
	"context"
	"testing"

	"nudgebee/services/internal/testenv"
)

const classificationReviewTestTenant = "b2ca6e00-0000-4000-8000-000000000003"

func TestRecordUncertainClassification_UpsertSemantics(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_classification_review WHERE tenant_id = $1::uuid`, classificationReviewTestTenant)
	})

	base := UncertainClassificationCandidate{
		TenantID:           classificationReviewTestTenant,
		Source:             "traces",
		ClassificationKind: "node_type",
		CandidateType:      "rabbitmq",
		CandidateName:      "services-server",
		CandidateNamespace: "example-ns",
		CandidateCluster:   "k8s-dev",
		ReasonCode:         "outbound_span_kind",
		ReasonDescription:  "test",
		Evidence:           map[string]interface{}{"span_name": "rabbitmq.consume"},
	}

	RecordUncertainClassification(ctx, dbm, base)

	var firstSeen, lastSeen string
	var occurrenceCount int
	var reasonCode, candidateType string
	row, err := dbm.QueryRow(`
		SELECT occurrence_count, reason_code, candidate_type, first_seen_at, last_seen_at
		FROM knowledge_graph_classification_review
		WHERE tenant_id = $1::uuid AND source = $2 AND classification_kind = $3
		  AND candidate_name = $4 AND candidate_namespace = $5`,
		base.TenantID, base.Source, base.ClassificationKind, base.CandidateName, base.CandidateNamespace)
	if err != nil {
		t.Fatalf("query after first insert: %v", err)
	}
	if err := row.Scan(&occurrenceCount, &reasonCode, &candidateType, &firstSeen, &lastSeen); err != nil {
		t.Fatalf("scan after first insert: %v", err)
	}
	if occurrenceCount != 1 {
		t.Errorf("occurrence_count after first insert = %d, expected 1", occurrenceCount)
	}
	if reasonCode != "outbound_span_kind" {
		t.Errorf("reason_code after first insert = %q, expected %q", reasonCode, "outbound_span_kind")
	}

	// Second occurrence, different reason/candidate_type — latest-wins overwrite.
	second := base
	second.ReasonCode = "missing_span_kind_with_verb"
	second.CandidateType = "kafka"
	second.Evidence = map[string]interface{}{"span_name": "kafka.consume"}
	RecordUncertainClassification(ctx, dbm, second)

	var occurrenceCount2 int
	var reasonCode2, candidateType2, firstSeen2, lastSeen2 string
	row2, err := dbm.QueryRow(`
		SELECT occurrence_count, reason_code, candidate_type, first_seen_at, last_seen_at
		FROM knowledge_graph_classification_review
		WHERE tenant_id = $1::uuid AND source = $2 AND classification_kind = $3
		  AND candidate_name = $4 AND candidate_namespace = $5`,
		base.TenantID, base.Source, base.ClassificationKind, base.CandidateName, base.CandidateNamespace)
	if err != nil {
		t.Fatalf("query after second insert: %v", err)
	}
	if err := row2.Scan(&occurrenceCount2, &reasonCode2, &candidateType2, &firstSeen2, &lastSeen2); err != nil {
		t.Fatalf("scan after second insert: %v", err)
	}
	if occurrenceCount2 != 2 {
		t.Errorf("occurrence_count after second occurrence = %d, expected 2", occurrenceCount2)
	}
	if reasonCode2 != "missing_span_kind_with_verb" {
		t.Errorf("reason_code after second occurrence = %q, expected latest value %q", reasonCode2, "missing_span_kind_with_verb")
	}
	if candidateType2 != "kafka" {
		t.Errorf("candidate_type after second occurrence = %q, expected latest value %q", candidateType2, "kafka")
	}
	if firstSeen2 != firstSeen {
		t.Errorf("first_seen_at changed across occurrences: %q -> %q, expected preserved", firstSeen, firstSeen2)
	}
	if lastSeen2 == lastSeen {
		t.Errorf("last_seen_at did not advance across occurrences")
	}

	// A different classification_kind for the same resource must be a
	// separate row, not a collision with the node_type row above.
	specificTypeVariant := base
	specificTypeVariant.ClassificationKind = "specific_type"
	specificTypeVariant.ReasonCode = "no_specific_type_mapping"
	RecordUncertainClassification(ctx, dbm, specificTypeVariant)

	var totalRows int
	countRow, err := dbm.QueryRow(`
		SELECT count(*) FROM knowledge_graph_classification_review
		WHERE tenant_id = $1::uuid AND candidate_name = $2 AND candidate_namespace = $3`,
		base.TenantID, base.CandidateName, base.CandidateNamespace)
	if err != nil {
		t.Fatalf("query row count: %v", err)
	}
	if err := countRow.Scan(&totalRows); err != nil {
		t.Fatalf("scan row count: %v", err)
	}
	if totalRows != 2 {
		t.Errorf("expected 2 rows (one per classification_kind) for the same resource, got %d", totalRows)
	}

	// Same tenant/source/kind/name/namespace in a DIFFERENT cluster must also
	// be a separate row: a tenant running the same workload name in two
	// clusters is the normal case, and collapsing them would leave
	// candidate_cluster reflecting whichever cluster was processed last while
	// occurrence_count silently summed across both.
	otherCluster := base
	otherCluster.CandidateCluster = "k8s-prod"
	RecordUncertainClassification(ctx, dbm, otherCluster)

	var clusterRows int
	clusterRow, err := dbm.QueryRow(`
		SELECT count(*) FROM knowledge_graph_classification_review
		WHERE tenant_id = $1::uuid AND source = $2 AND classification_kind = $3
		  AND candidate_name = $4 AND candidate_namespace = $5`,
		base.TenantID, base.Source, base.ClassificationKind, base.CandidateName, base.CandidateNamespace)
	if err != nil {
		t.Fatalf("query per-cluster row count: %v", err)
	}
	if err := clusterRow.Scan(&clusterRows); err != nil {
		t.Fatalf("scan per-cluster row count: %v", err)
	}
	if clusterRows != 2 {
		t.Errorf("expected 2 rows (one per cluster) for the same resource and kind, got %d", clusterRows)
	}

	var otherClusterCount int
	otherRow, err := dbm.QueryRow(`
		SELECT occurrence_count FROM knowledge_graph_classification_review
		WHERE tenant_id = $1::uuid AND source = $2 AND classification_kind = $3
		  AND candidate_name = $4 AND candidate_namespace = $5 AND candidate_cluster = $6`,
		base.TenantID, base.Source, base.ClassificationKind, base.CandidateName,
		base.CandidateNamespace, otherCluster.CandidateCluster)
	if err != nil {
		t.Fatalf("query other-cluster row: %v", err)
	}
	if err := otherRow.Scan(&otherClusterCount); err != nil {
		t.Fatalf("scan other-cluster row: %v", err)
	}
	if otherClusterCount != 1 {
		t.Errorf("occurrence_count for the second cluster = %d, expected 1 (not summed with the first cluster)", otherClusterCount)
	}
}

func TestRecordUncertainClassification_MissingRequiredFieldsIsNoop(t *testing.T) {
	dbm := testenv.RequireMetastore(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = dbm.Exec(`DELETE FROM knowledge_graph_classification_review WHERE tenant_id = $1::uuid`, classificationReviewTestTenant)
	})

	// Missing ReasonCode — must not panic and must not insert anything.
	RecordUncertainClassification(ctx, dbm, UncertainClassificationCandidate{
		TenantID:           classificationReviewTestTenant,
		Source:             "traces",
		ClassificationKind: "node_type",
		CandidateName:      "incomplete-candidate",
	})

	var count int
	row, err := dbm.QueryRow(`SELECT count(*) FROM knowledge_graph_classification_review WHERE tenant_id = $1::uuid`, classificationReviewTestTenant)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no row written when ReasonCode is missing, got %d", count)
	}
}
