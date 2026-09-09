package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/triage"

	"github.com/stretchr/testify/require"
)

// This asserts the answer the screen gives, not the value a function returns.
//
// Every earlier test in this subsystem checked an intermediate: does the seed
// resolve, does the lookup find a node. Those passed while the card said
// "4 depend on this one — none of them alerted" on a response that reported two
// of those dependents alerting. The tier the UI actually reads was empty, and
// nothing tested the tier.
//
// The cause: the topology map was keyed by the graph node's name
// (nudgebee-scenario-services-payment) while alert identities are keyed by the
// event's subject, which for a cloud alarm is the provider id
// (i-0b079820a95b1517a). AssembleTiers compares the two, so nothing matched.
//
// buildTopologyMap mirrors the keying in handleEventGetImpact; if that keying
// regresses, the impact tier silently empties again and this fails.
func buildTopologyMap(seedKey string, dependents []core.ImpactedService) map[string][]string {
	m := map[string][]string{}
	topoKeys := func(s core.ImpactedService) []string {
		keys := []string{triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: s.Namespace, SubjectName: s.Name})}
		if s.ResourceID != "" && s.ResourceID != s.Name {
			keys = append(keys, triage.SubjectKey(triage.AlertIdentity{
				SubjectNamespace: s.Namespace, SubjectName: s.ResourceID}))
		}
		return keys
	}
	for _, d := range dependents {
		for _, k := range topoKeys(d) {
			m[k] = append(m[k], seedKey)
		}
	}
	return m
}

func TestCloudDependentsReachTheImpactTier(t *testing.T) {
	// The dev cascade: order is the seed, payment and inventory call it. Node
	// names come from the Name tag; the alarms name their subjects by instance id.
	seed := triage.AlertIdentity{
		ID: "evt-order", SubjectName: "i-0dcee3621b8456783", SubjectType: "compute-instance",
	}
	dependents := []core.ImpactedService{
		{Name: "nudgebee-scenario-services-payment", ResourceID: "i-0b079820a95b1517a",
			NodeType: core.NodeTypeComputeInstance, HopsAway: 1},
		{Name: "nudgebee-scenario-services-inventory", ResourceID: "i-00e845d10772a9058",
			NodeType: core.NodeTypeComputeInstance, HopsAway: 1},
	}
	// The alerts as the events table holds them — subject is the instance id.
	candidates := []triage.AlertIdentity{
		{ID: "evt-payment", SubjectName: "i-0b079820a95b1517a", SubjectType: "compute-instance"},
		{ID: "evt-inventory", SubjectName: "i-00e845d10772a9058", SubjectType: "compute-instance"},
	}

	topology := buildTopologyMap(triage.SubjectKey(seed), dependents)
	tiers := triage.AssembleTiers(seed, candidates, topology, nil)

	for _, c := range candidates {
		require.Equalf(t, "impact", tiers[c.ID],
			"%s calls the seed and alerted in the window, but landed in tier %q.\n"+
				"An empty impact tier is what makes the UI say \"none of them alerted\" "+
				"while the same response reports the dependent alerting. Check that the "+
				"topology map is keyed by the dependent's ResourceID as well as its name.",
			c.SubjectName, tiers[c.ID])
	}
}

// Keying by name must keep working: on Kubernetes the node name and the event
// subject are the same string, and there is no ResourceID at all.
func TestKubernetesDependentsStillReachTheImpactTier(t *testing.T) {
	seed := triage.AlertIdentity{ID: "evt-cart", SubjectName: "cart", SubjectNamespace: "otel-demo"}
	dependents := []core.ImpactedService{
		{Name: "frontend", Namespace: "otel-demo", NodeType: core.NodeTypeWorkload, HopsAway: 1},
	}
	candidates := []triage.AlertIdentity{
		{ID: "evt-frontend", SubjectName: "frontend", SubjectNamespace: "otel-demo"},
	}

	tiers := triage.AssembleTiers(seed, candidates, buildTopologyMap(triage.SubjectKey(seed), dependents), nil)
	require.Equal(t, "impact", tiers["evt-frontend"],
		"a k8s dependent must still reach the impact tier through its name")
}

// An unrelated alert must not be pulled in: a test that only checks the positive
// case would pass on a map that connected everything to everything.
func TestUnrelatedAlertDoesNotReachTheImpactTier(t *testing.T) {
	seed := triage.AlertIdentity{ID: "evt-order", SubjectName: "i-0dcee3621b8456783"}
	dependents := []core.ImpactedService{
		{Name: "nudgebee-scenario-services-payment", ResourceID: "i-0b079820a95b1517a", HopsAway: 1},
	}
	candidates := []triage.AlertIdentity{
		{ID: "evt-stranger", SubjectName: "i-0999999999999999"},
	}

	tiers := triage.AssembleTiers(seed, candidates, buildTopologyMap(triage.SubjectKey(seed), dependents), nil)
	require.NotEqual(t, "impact", tiers["evt-stranger"],
		"an instance that does not call the seed must not be reported as impacted")
}

// The first version of this guard compared the map against the very lists it was
// built from, so an empty map implied empty lists and the branch was unreachable
// — a silent no-op inside the check that exists to catch silent no-ops. These
// pin that it can actually fire, and that it stays quiet when it should.
func TestTopologyLostItsDependentsFires(t *testing.T) {
	// The real failure: the graph found dependents, none reached the map.
	impact := &core.ImpactSummary{
		Dependents: []core.ImpactedService{{Name: "payment", ResourceID: "i-0b079820a95b1517a"}},
	}
	require.True(t, topologyLostItsDependents(map[string][]string{}, impact),
		"dependents in the graph and an empty topology map is the exact condition "+
			"this reports; if it cannot fire, the next identity bug is found by "+
			"reading a screenshot again")

	// Infrastructure-only dependents count: on a VM stack that is every dependent.
	infraOnly := &core.ImpactSummary{
		InfrastructureDependents: []core.ImpactedService{{Name: "order", ResourceID: "i-0dcee3621b8456783"}},
	}
	require.True(t, topologyLostItsDependents(map[string][]string{}, infraOnly))
}

func TestTopologyLostItsDependentsStaysQuiet(t *testing.T) {
	withDeps := &core.ImpactSummary{
		Dependents: []core.ImpactedService{{Name: "payment", ResourceID: "i-0b079820a95b1517a"}},
	}
	// The healthy case — dependents made it into the map.
	require.False(t, topologyLostItsDependents(
		map[string][]string{"\x00i-0b079820a95b1517a": {"\x00i-0dcee3621b8456783"}}, withDeps))

	// A genuinely isolated resource has no dependents and no topology. That is an
	// answer, not a defect, and reporting it would bury the real ones in noise.
	require.False(t, topologyLostItsDependents(map[string][]string{}, &core.ImpactSummary{}))

	// An unresolved seed never gets a summary.
	require.False(t, topologyLostItsDependents(map[string][]string{}, nil))
}
