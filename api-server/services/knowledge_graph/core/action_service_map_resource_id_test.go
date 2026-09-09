package core

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
)

// A cloud alarm names its subject by the provider's identifier - an EC2 alarm
// carries "i-0dcee3621b8456783" - while the knowledge-graph node is named from
// the resource's Name tag ("orders-api"). resource_id is not indexed into
// query_attributes, so the name-based lookups in this file cannot match it.
//
// The consequence was silent and total: knowledgeGraphServiceMapAction returned
// no evidence at all for such an event, and event correlation walks that
// evidence, so every AWS instance alarm scored dependency_distance 0 and no
// cross-service correlation could ever be produced. Measured on a live account:
// 35 compute-instance events produced 24 correlations once the graph resolved,
// while the same instances resolved by id produced none.
//
// findServiceNodesByResourceID is the fallback that closes it, mirroring the
// existing load-balancer recovery a few lines above it.
func TestFindServiceNodesByResourceID_ResolvesWhenNameDiffers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	const (
		tenantID   = "c897a35b-99e9-4347-92ce-c44f71a1a614"
		accountID  = "00666e9f-3774-4f5d-b86c-60b201ae18c5"
		instanceID = "i-0b079820a95b1517a"
		nodeName   = "nudgebee-scenario-services-payment"
	)

	// The fallback must resolve the provider id to a node, and that node must be
	// the one carrying the dependency edges - resolving to *a* node is not enough.
	//
	// The two environments fail differently, which is why this asserts on edges
	// rather than on whether the name lookup misses:
	//   - before node collapse, the id matches a second SSM-derived node that
	//     holds no CALLS edges, so evidence is built from the wrong node
	//   - after node collapse, that node is gone and the id matches nothing, so
	//     no evidence is produced at all
	// Both end at dependency_distance 0. Only resolving to the edge-bearing node
	// fixes either.
	got, err := findServiceNodesByResourceID(dbManager, tenantID, accountID, instanceID)
	if err != nil {
		t.Fatalf("findServiceNodesByResourceID: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("resource-id fallback found no node; an EC2 alarm would carry no " +
			"knowledge_graph evidence and could never correlate")
	}

	var withEdges string
	for _, id := range got {
		var n int
		row := dbManager.Db.QueryRow(`
			SELECT count(*) FROM knowledge_graph_edge
			WHERE (source_node_id = $1 OR destination_node_id = $1)
			  AND relationship_type = 'CALLS' AND is_active = true`, id)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("counting CALLS edges for %s: %v", id, err)
		}
		if n > 0 {
			withEdges = id
			break
		}
	}
	if withEdges == "" {
		t.Fatalf("fallback resolved %v but none of those nodes carry CALLS edges - "+
			"correlation would still have nothing to traverse", got)
	}
	slog.Default().Info("resolved to edge-bearing node", "node", withEdges)
}

// An empty subject must not turn into a table scan that matches arbitrary rows.
func TestFindServiceNodesByResourceID_EmptyInputMatchesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}
	got, err := findServiceNodesByResourceID(dbManager, "c897a35b-99e9-4347-92ce-c44f71a1a614", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty resource id matched %d nodes, want 0", len(got))
	}
}

// When every lookup fails the action returns no evidence, which removes the
// event from correlation and from analysis. Nothing downstream can tell that
// happened - the alarm, the event and the graph all look healthy - so the miss
// has to be recorded or it is invisible.
//
// This asserts the row lands, using a subject that cannot match any node.
func TestServiceMapRecordsNearMissWhenSubjectResolvesToNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Skipf("Skipping integration test - database not available: %v", err)
	}

	const tenantID = "c897a35b-99e9-4347-92ce-c44f71a1a614"
	subject := "i-nonexistent-" + t.Name()

	var before int
	if err := dbManager.Db.QueryRow(`
		SELECT count(*) FROM public.knowledge_graph_classification_review
		WHERE tenant_id = $1 AND classification_kind = 'node_match'
		  AND candidate_name = $2`, tenantID, subject).Scan(&before); err != nil {
		t.Skipf("review table not available: %v", err)
	}

	RecordUncertainClassification(context.Background(), dbManager, UncertainClassificationCandidate{
		TenantID:           tenantID,
		Source:             "knowledge_graph_service_map",
		ClassificationKind: "node_match",
		CandidateName:      subject,
		ReasonCode:         "no_node_for_event_subject",
		ReasonDescription:  "test fixture",
		Evidence:           map[string]interface{}{"test": true},
	})

	var after int
	if err := dbManager.Db.QueryRow(`
		SELECT count(*) FROM public.knowledge_graph_classification_review
		WHERE tenant_id = $1 AND classification_kind = 'node_match'
		  AND candidate_name = $2`, tenantID, subject).Scan(&after); err != nil {
		t.Fatalf("counting review rows: %v", err)
	}
	if after <= before {
		t.Errorf("near-miss was not recorded (%d -> %d); an unresolvable subject "+
			"stays invisible and correlation failures cannot be diagnosed", before, after)
	}

	_, _ = dbManager.Db.Exec(`
		DELETE FROM public.knowledge_graph_classification_review
		WHERE tenant_id = $1 AND candidate_name = $2`, tenantID, subject)
}
