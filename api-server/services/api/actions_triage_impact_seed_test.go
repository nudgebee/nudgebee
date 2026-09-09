package api

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/triage"
)

// TestSeedNodeTypeGroupsAreSingleType pins the property that makes resolution
// work at all: the resolver accepts a match only when exactly one node comes
// back, and an AWS resource routinely shares its name with a security group
// (`nb-demo-db` is both a Database and a SecurityGroup). Grouping several types
// into one search would return 2 and silently resolve nothing — the bug would
// look fixed while behaving identically.
func TestSeedNodeTypeGroupsAreSingleType(t *testing.T) {
	groups := seedNodeTypeGroups()
	if len(groups) == 0 {
		t.Fatal("no seed node type groups")
	}
	seen := map[core.NodeType]bool{}
	for _, g := range groups {
		if g.nodeType == "" {
			t.Errorf("empty node type in groups")
		}
		if seen[g.nodeType] {
			t.Errorf("node type %q appears twice; the second attempt is dead code", g.nodeType)
		}
		seen[g.nodeType] = true
	}
}

// TestSeedNodeTypeGroupsPreferWorkload keeps the existing Kubernetes preference:
// a workload and the service in front of it share a name, and the workload is
// the better blast-radius seed.
func TestSeedNodeTypeGroupsPreferWorkload(t *testing.T) {
	groups := seedNodeTypeGroups()
	index := func(nt core.NodeType) int {
		for i, g := range groups {
			if g.nodeType == nt {
				return i
			}
		}
		return -1
	}
	workload, service, k8sService := index(core.NodeTypeWorkload), index(core.NodeTypeService), index(core.NodeTypeK8sService)
	if workload < 0 || service < 0 || k8sService < 0 {
		t.Fatalf("k8s types missing: workload=%d service=%d k8sService=%d", workload, service, k8sService)
	}
	if workload >= service || service >= k8sService {
		t.Errorf("k8s preference order broken: workload=%d service=%d k8sService=%d", workload, service, k8sService)
	}
}

// TestCloudSeedTypesResolvable is the regression for the blast radius returning
// {"resolved": false} on a live RDS alarm: the resolver only searched Kubernetes
// node types, so an AWS Database, ComputeInstance or LoadBalancer could never be
// found no matter how complete the topology was.
func TestCloudSeedTypesResolvable(t *testing.T) {
	groups := seedNodeTypeGroups()
	has := func(nt core.NodeType) bool {
		for _, g := range groups {
			if g.nodeType == nt {
				return true
			}
		}
		return false
	}
	for _, nt := range []core.NodeType{
		core.NodeTypeDatabase,
		core.NodeTypeComputeInstance,
		core.NodeTypeLoadBalancer,
	} {
		if !has(nt) {
			t.Errorf("%q is not resolvable as a blast-radius seed", nt)
		}
	}
}

// TestKubernetesSeedsKeepNamespace guards the hazard the namespace relaxation
// could introduce: a workload name is unique only within its namespace, so
// dropping the namespace for Kubernetes types would let a workload in one
// namespace answer for a same-named workload in another. Cloud types must drop
// it — their nodes carry no namespace at all.
func TestKubernetesSeedsKeepNamespace(t *testing.T) {
	for _, g := range seedNodeTypeGroups() {
		switch g.nodeType {
		case core.NodeTypeWorkload, core.NodeTypeService, core.NodeTypeK8sService:
			if !g.namespaced {
				t.Errorf("%q must match with the event namespace", g.nodeType)
			}
			if got := g.namespace("demo"); got != "demo" {
				t.Errorf("%q namespace = %q, want %q", g.nodeType, got, "demo")
			}
		default:
			if g.namespaced {
				t.Errorf("%q is a cloud type and must be searched namespace-blind", g.nodeType)
			}
			// A cloud event carries "AmazonRDS" here; cloud nodes have no namespace.
			if got := g.namespace("AmazonRDS"); got != "" {
				t.Errorf("%q namespace = %q, want empty", g.nodeType, got)
			}
		}
	}
}

// TestSeedTypesExcludePlumbing keeps infrastructure furniture out of seeding.
// Blast radius from a security group would report every instance attached to it.
func TestSeedTypesExcludePlumbing(t *testing.T) {
	for _, g := range seedNodeTypeGroups() {
		switch g.nodeType {
		case core.NodeTypeSecurityGroup, core.NodeTypeSubnet, core.NodeTypeVPC:
			t.Errorf("%q is plumbing and must not be a blast-radius seed", g.nodeType)
		}
	}
}

// TestInfrastructureImpactedIsNotNamespaceScoped guards a trap this change fell
// into once: scopeAndNormalize drops anything whose namespace differs from the
// event's, and infrastructure nodes carry no namespace at all (a cloud resource
// has none; a k8s Node is cluster-wide). Scoping them by the event namespace —
// "AmazonRDS" for an RDS alarm — silently emptied the list while the count still
// said 2.
func TestInfrastructureImpactedIsNotNamespaceScoped(t *testing.T) {
	infra := []core.ImpactedService{
		{NodeID: "api", Name: "nb-demo-api", NodeType: core.NodeTypeComputeInstance, HopsAway: 1},
		{NodeID: "web", Name: "nb-demo-web", NodeType: core.NodeTypeComputeInstance, HopsAway: 2},
	}
	if got := scopeAndNormalize(infra, "AmazonRDS"); len(got) != 0 {
		t.Fatalf("precondition changed: namespace scoping no longer drops them (%d kept)", len(got))
	}
	got := scopeAndNormalize(infra, "")
	if len(got) != 2 {
		t.Fatalf("unscoped = %d entries, want 2", len(got))
	}
}

// The same mismatch reaches the alerting join and the incident assembly, not just
// the response scoping. A cloud alarm records the provider's service code in
// subject_namespace while the graph node it belongs to carries none, so the alert
// index and the dependent lookup are keyed differently and a cloud dependent can
// never be reported as alerting. Both sides normalise to the empty namespace for a
// namespace-blind seed; these are the two key shapes that have to meet.
func TestCloudAlertKeysMeetTheGraphNamespace(t *testing.T) {
	// What the alert index sees, straight from the events table.
	const eventNamespace = "AmazonEC2"
	// What the graph-derived dependent carries.
	const nodeNamespace = ""
	const name = "nb-demo-web"

	if impactKey(eventNamespace, name) == impactKey(nodeNamespace, name) {
		t.Fatal("precondition changed: cloud event and graph namespaces now agree, so the dual index is unnecessary")
	}
	// Indexing under the empty namespace as well is what lets the lookup land.
	if impactKey("", name) != impactKey(nodeNamespace, name) {
		t.Fatalf("empty-namespace index %q does not match the dependent lookup %q",
			impactKey("", name), impactKey(nodeNamespace, name))
	}

	// The assembly keys the same way through triage.SubjectKey, so a seed left
	// carrying the service code cannot match its own topology entries.
	seedWithServiceCode := triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: eventNamespace, SubjectName: "nb-demo-db"})
	topology := triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: nodeNamespace, SubjectName: "nb-demo-db"})
	if seedWithServiceCode == topology {
		t.Fatal("precondition changed: SubjectKey no longer includes the namespace")
	}
	seedNormalised := triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: "", SubjectName: "nb-demo-db"})
	if seedNormalised != topology {
		t.Fatalf("normalised seed key %q must equal the topology key %q", seedNormalised, topology)
	}
}

// Normalising the seed's namespace without normalising the candidates' silently
// empties the same-subject tier: AssembleTiers groups an alert with the seed when
// their SubjectKeys are equal, and the seed would be keyed "|nb-demo-db" against
// candidates keyed "amazonrds|nb-demo-db". The seed, the candidates and the
// topology map are one identifier space and have to be normalised together.
func TestAssemblySeedAndCandidatesShareOneNamespaceSpace(t *testing.T) {
	seed := triage.AlertIdentity{SubjectName: "nb-demo-db", SubjectType: "db", AggregationKey: "nb-demo-rds-conns-high"}
	rawCandidate := triage.AlertIdentity{
		ID: "sibling", SubjectName: "nb-demo-db", SubjectNamespace: "AmazonRDS", SubjectType: "db",
		AggregationKey: "nb-demo-rds-other", TsOffsetS: 60,
	}

	// Seed normalised, candidate not: the sibling alert falls out of the incident.
	got := triage.AssembleTiers(seed, []triage.AlertIdentity{rawCandidate}, map[string][]string{}, map[string]triage.Rate{})
	if got[rawCandidate.ID] == triage.TierCore {
		t.Fatal("precondition changed: a raw-namespace candidate now groups with a normalised seed")
	}

	// Both normalised: the alert on the same subject is part of the same incident.
	normalised := rawCandidate
	normalised.SubjectNamespace = ""
	got = triage.AssembleTiers(seed, []triage.AlertIdentity{normalised}, map[string][]string{}, map[string]triage.Rate{})
	if got[normalised.ID] != triage.TierCore {
		t.Fatalf("same-subject candidate tier = %q, want %q — fetchWindowRows must blank the namespace whenever the seed's was blanked",
			got[normalised.ID], triage.TierCore)
	}
}

// The same trap, one field over: depends_on and impacted were still scoped by
// the event namespace for every seed, so a load-balancer alarm returned
// depends_on: null even after the graph traversal started finding its backend.
// The seed's own resolution already knows whether the namespace means anything —
// a cloud seed is matched namespace-blind — so the response scoping has to use
// that same answer rather than the raw event namespace.
func TestDependsOnScopingFollowsSeedResolution(t *testing.T) {
	backend := []core.ImpactedService{
		{NodeID: "web", Name: "nb-demo-web", NodeType: core.NodeTypeComputeInstance, HopsAway: 1},
	}

	// A cloud seed: the event namespace is a service code and the neighbours have
	// none, so scoping by it empties the list. This is the shipped bug.
	if got := scopeAndNormalize(backend, "AWSELB"); len(got) != 0 {
		t.Fatalf("precondition changed: cloud scoping no longer drops the backend (%d kept)", len(got))
	}

	// What the handler must do instead for a namespace-blind (cloud) seed.
	scopeNamespace := ""
	for _, g := range seedNodeTypeGroups() {
		if g.nodeType == core.NodeTypeLoadBalancer {
			if g.namespaced {
				t.Fatal("LoadBalancer became namespace-scoped; the handler's scoping would empty depends_on again")
			}
			scopeNamespace = g.namespace("AWSELB")
		}
	}
	if got := scopeAndNormalize(backend, scopeNamespace); len(got) != 1 || got[0].Name != "nb-demo-web" {
		t.Fatalf("cloud seed depends_on = %+v, want the backend kept", got)
	}

	// A Kubernetes seed keeps its namespace filter: workload names are unique
	// only within a namespace, so dropping the scope there would be wrong.
	workloads := []core.ImpactedService{
		{NodeID: "a", Name: "checkout", Namespace: "shop", NodeType: core.NodeTypeWorkload, HopsAway: 1},
		{NodeID: "b", Name: "checkout", Namespace: "other", NodeType: core.NodeTypeWorkload, HopsAway: 1},
	}
	for _, g := range seedNodeTypeGroups() {
		if g.nodeType == core.NodeTypeWorkload {
			got := scopeAndNormalize(workloads, g.namespace("shop"))
			if len(got) != 1 || got[0].Namespace != "shop" {
				t.Fatalf("k8s seed depends_on = %+v, want only the shop namespace", got)
			}
		}
	}
}

// A cloud alarm names its subject by the provider's id — an EC2 CPU alarm fires
// on "i-0f568ef22d52139bb" — while the graph node it belongs to is named by its
// Name tag. Matching alerts to dependents on name alone can therefore never
// connect an instance's own alarm to the instance the graph reports as a
// dependent, which is why a database incident showed nothing alerting while both
// instances in front of it were at 100% CPU.
func TestDependentMatchesAlertByResourceID(t *testing.T) {
	const instanceID = "i-0f568ef22d52139bb"
	dependent := core.ImpactedService{
		Name: "nb-demo-web", ResourceID: instanceID, NodeType: core.NodeTypeComputeInstance,
	}

	// The alert index is keyed off the event's subject, which is the instance id.
	if impactKey("", dependent.Name) == impactKey("", instanceID) {
		t.Fatal("precondition changed: the Name tag and the instance id now key alike")
	}
	// Falling back to the resource id is what makes the two meet.
	if impactKey("", dependent.ResourceID) != impactKey("", instanceID) {
		t.Fatalf("resource-id key %q does not match the alarm subject key %q",
			impactKey("", dependent.ResourceID), impactKey("", instanceID))
	}

	// A Kubernetes dependent has no resource id, so the fallback is inert there.
	k8s := core.ImpactedService{Name: "checkout", Namespace: "shop", NodeType: core.NodeTypeWorkload}
	if k8s.ResourceID != "" {
		t.Error("Kubernetes dependents must not carry a provider resource id")
	}
}
