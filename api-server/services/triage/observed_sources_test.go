package triage_test

import (
	"testing"

	"nudgebee/services/knowledge_graph/flow_sources"
	"nudgebee/services/triage"

	"github.com/stretchr/testify/assert"
)

// Every source the knowledge graph can stamp on an edge, other than a user's own
// declaration, is an observation. A source missing from the set scores its edges
// as user-declared, and the comment that asked for the two lists to be kept in
// sync did not stop it.
func TestObservedSourcesForWeighting_CoverEdgePriorityTable(t *testing.T) {
	for relType, sources := range flow_sources.EdgeTypePriorities {
		for source := range sources {
			if source == "manual" {
				continue
			}
			assert.True(t, triage.ObservedSourcesForWeighting[source],
				"source %q (on %s edges) is missing from observedSourcesForWeighting", source, relType)
		}
	}
}
