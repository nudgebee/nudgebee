package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/flow_sources"

	"github.com/stretchr/testify/assert"
)

// manualDepSummary feeds audit.LogChange from repository return values. Today
// every one of those calls returns a non-nil value whenever err is nil (a
// missing row surfaces as sql.ErrNoRows, not a nil pair), so the guard is
// deliberate insurance rather than a live bug: the audit call sits AFTER the
// handler's error check, so a future (nil, nil) would panic inside a request
// that had already succeeded.
func TestManualDepSummaryHandlesNil(t *testing.T) {
	assert.Nil(t, manualDepSummary(nil))
}

// The audit row has to identify which declaration changed without storing the
// whole struct — both endpoints, the relationship and how it resolved.
func TestManualDepSummaryCarriesIdentifyingFields(t *testing.T) {
	got := manualDepSummary(&flow_sources.ManualDependency{
		ID:               42,
		SourceNodeType:   "Workload",
		SourceName:       "checkout",
		DestNodeType:     "RDSInstance",
		DestName:         "orders-db",
		RelationshipType: "CALLS",
		ResolutionStatus: "resolved",
		Notes:            "unused by the audit row",
	})

	assert.Equal(t, map[string]any{
		"id":                int64(42),
		"source_name":       "checkout",
		"source_node_type":  "Workload",
		"dest_name":         "orders-db",
		"dest_node_type":    "RDSInstance",
		"relationship_type": "CALLS",
		"resolution_status": "resolved",
	}, got)
}
