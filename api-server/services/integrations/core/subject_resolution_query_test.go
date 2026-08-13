package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildWorkloadLookupQuery_NamespaceScoping pins the #34569 fix: when the
// event's namespace is known the lookup must be namespace-qualified so a workload
// name present in several namespaces resolves to the row for the event's own
// namespace instead of an arbitrary LIMIT 1 pick (which collapsed cloud_resource_id
// across namespaces and produced false same_resource correlations).
func TestBuildWorkloadLookupQuery_NamespaceScoping(t *testing.T) {
	const namePredicate = `name = $3`

	scoped := buildWorkloadLookupQuery(namePredicate, true)
	assert.Contains(t, scoped, namePredicate, "predicate must be embedded")
	assert.Contains(t, scoped, "AND namespace = $4", "namespace clause required when scoped")
	assert.Contains(t, scoped, workloadLookupFilter, "activeness/kind filter must remain")
	assert.True(t, strings.HasSuffix(scoped, " LIMIT 1"), "must terminate with LIMIT 1")

	unscoped := buildWorkloadLookupQuery(namePredicate, false)
	assert.Contains(t, unscoped, namePredicate)
	assert.NotContains(t, unscoped, "namespace = $4", "no namespace clause when namespace unknown")
	assert.NotContains(t, unscoped, "$4", "no fourth positional arg when namespace unknown")
	assert.True(t, strings.HasSuffix(unscoped, " LIMIT 1"))
}

// TestBuildWorkloadLookupQuery_LabelPredicates confirms the label-based strategies
// are namespace-qualified the same way as the exact-name strategy.
func TestBuildWorkloadLookupQuery_LabelPredicates(t *testing.T) {
	for _, pred := range []string{
		`labels->>'app.kubernetes.io/name' = $3`,
		`labels->>'app' = $3`,
		`labels->>'tags.datadoghq.com/service' = $3`,
	} {
		q := buildWorkloadLookupQuery(pred, true)
		assert.Contains(t, q, pred)
		assert.Contains(t, q, "AND namespace = $4", "label strategy %q must also be namespace-scoped", pred)
	}
}
