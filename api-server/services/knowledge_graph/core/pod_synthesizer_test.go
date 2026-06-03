package core

import (
	"testing"
	"time"
)

// TestIncludesPod covers the node-type filter that decides whether
// Pod fan-out runs in GetMultipleNodeNeighbors / TraverseDirectional /
// SearchNodes. Empty filter ⇒ no restriction ⇒ Pods allowed.
func TestIncludesPod(t *testing.T) {
	cases := []struct {
		name string
		in   []NodeType
		want bool
	}{
		{"empty filter passes Pod", nil, true},
		{"empty slice passes Pod", []NodeType{}, true},
		{"explicit Pod", []NodeType{NodeTypePod}, true},
		{"Pod mixed with others", []NodeType{NodeTypeNode, NodeTypePod, NodeTypeWorkload}, true},
		{"only other types excludes Pod", []NodeType{NodeTypeNode, NodeTypeWorkload}, false},
		{"unrelated type only", []NodeType{NodeTypeCluster}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := includesPod(tc.in); got != tc.want {
				t.Errorf("includesPod(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestSupportedManagesKinds pins the exact set of workload_type values
// for which the synthesizer emits a Workload→Pod MANAGES edge. The
// audit of public.k8s_pods showed that production traffic in unsupported
// kinds (ReplicaSet, Runner, Node, Rollout) accounts for 88k+ pods —
// those must NOT match here, or callers would create dangling edges.
func TestSupportedManagesKinds(t *testing.T) {
	must := []string{"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}
	for _, k := range must {
		if !supportedManagesKinds[k] {
			t.Errorf("supportedManagesKinds[%q] = false, want true", k)
		}
	}
	mustNot := []string{"ReplicaSet", "Runner", "Node", "Rollout", "Pod", ""}
	for _, k := range mustNot {
		if supportedManagesKinds[k] {
			t.Errorf("supportedManagesKinds[%q] = true, want false", k)
		}
	}
}

// TestCapLimit covers the hard ceiling on synth fan-out. A caller
// asking for 0 (the "no explicit cap" sentinel) must still be bounded
// so a Workload with 10k replicas can't blow the response.
func TestCapLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, hardPodCap},
		{-1, hardPodCap},
		{1, 1},
		{499, 499},
		{500, 500},
		{501, hardPodCap},
		{10_000, hardPodCap},
	}
	for _, tc := range cases {
		if got := capLimit(tc.in); got != tc.want {
			t.Errorf("capLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestPodRowToNode covers the row → DbNode shape that downstream KG
// consumers depend on. The big risk is mis-typing the ID (must equal
// cloud_resource_id verbatim for LLM round-tripping) or omitting a key
// that ExtractQueryAttributes(NodeTypePod, …) expects.
func TestPodRowToNode(t *testing.T) {
	created := time.Date(2026, 6, 1, 11, 59, 29, 0, time.UTC)
	seen := time.Date(2026, 6, 1, 12, 15, 30, 0, time.UTC)
	helm := true

	row := &podRow{
		CloudResourceID: "11111111-1111-5111-8111-111111111111",
		TenantID:        "22222222-2222-2222-2222-222222222222",
		CloudAccountID:  "33333333-3333-3333-3333-333333333333",
		Name:            "gke-metrics-agent-l88cs",
		Namespace:       "kube-system",
		Status:          "Running",
		NodeName:        "gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-fwsp",
		WorkloadType:    "DaemonSet",
		WorkloadName:    "gke-metrics-agent",
		CreationTime:    created,
		LastSeen:        seen,
		Labels:          map[string]string{"app": "gke-metrics-agent"},
		PodIP:           "10.244.1.42",
		HostIP:          "10.0.1.100",
		IsHelmRelease:   &helm,
	}

	got := row.node("k8s-dev")
	if got == nil {
		t.Fatal("node() returned nil")
	}

	if got.ID != row.CloudResourceID {
		t.Errorf("ID = %q, want %q (synth ID must equal cloud_resource_id for LLM round-trip)", got.ID, row.CloudResourceID)
	}
	if got.NodeType != NodeTypePod {
		t.Errorf("NodeType = %q, want %q", got.NodeType, NodeTypePod)
	}
	if got.Source != "k8s" {
		t.Errorf("Source = %q, want %q", got.Source, "k8s")
	}
	if got.Level != "Tenant" {
		t.Errorf("Level = %q, want %q", got.Level, "Tenant")
	}
	if got.TenantID != row.TenantID || got.CloudAccountID != row.CloudAccountID {
		t.Errorf("tenant/account mismatch: got (%s,%s) want (%s,%s)",
			got.TenantID, got.CloudAccountID, row.TenantID, row.CloudAccountID)
	}

	// QueryAttributes is what every SQL filter (`query_attributes->>'name'`
	// etc.) reads. If extraction silently drops fields, traversal seeds
	// and searches stop working for synth Pods.
	wantQAStrings := map[string]string{
		"name":      "gke-metrics-agent-l88cs",
		"namespace": "kube-system",
		"cluster":   "k8s-dev",
		"phase":     "Running",
		"node_name": "gke-nudgebee-dev-runner-node-pool-v2-8f4d877c-fwsp",
		"pod_ip":    "10.244.1.42",
		"host_ip":   "10.0.1.100",
	}
	for k, want := range wantQAStrings {
		if got.QueryAttributes[k] != want {
			t.Errorf("QueryAttributes[%q] = %v, want %q", k, got.QueryAttributes[k], want)
		}
	}

	if got.Labels["app"] != "gke-metrics-agent" {
		t.Errorf("Labels[app] = %q, want %q", got.Labels["app"], "gke-metrics-agent")
	}

	// CreatedAt / UpdatedAt come from the pod row, not time.Now(); this
	// keeps the response stable for caching and avoids spurious "node
	// modified" signals to consumers.
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(seen) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, seen)
	}

	// is_helm_release is a tri-state value: present-true / present-false
	// / absent. A *bool=nil row must produce no property, not "false".
	if v, ok := got.Properties["is_helm_release"]; !ok || v != true {
		t.Errorf("is_helm_release: ok=%v val=%v, want ok=true val=true", ok, v)
	}
}

// TestPodRowToNode_EmptyCluster covers the early-onboarding case where
// no k8s_source Cluster entity exists yet. The synth Pod should still
// be returned (callers can decide what to do); cluster just gets omitted
// from properties / query_attributes rather than being stored as "".
func TestPodRowToNode_EmptyCluster(t *testing.T) {
	row := &podRow{
		CloudResourceID: "11111111-1111-5111-8111-111111111111",
		TenantID:        "22222222-2222-2222-2222-222222222222",
		CloudAccountID:  "33333333-3333-3333-3333-333333333333",
		Name:            "p", Namespace: "ns", Status: "Running",
		LastSeen: time.Now(),
	}
	got := row.node("")
	if got == nil {
		t.Fatal("node() returned nil")
	}
	if _, ok := got.Properties["cluster"]; ok {
		t.Error("cluster should be omitted from properties when empty")
	}
	if _, ok := got.QueryAttributes["cluster"]; ok {
		t.Error("cluster should be omitted from query_attributes when empty")
	}
}

// TestSynthEdge_DeterministicID pins the property that two calls
// producing the same logical edge return the same ID. The induced
// subgraph paths rely on this to dedup naturally.
func TestSynthEdge_DeterministicID(t *testing.T) {
	a, b := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	now := time.Now()
	e1 := synthEdge(a, b, RelationshipRunsOn, "t", "acct", now)
	e2 := synthEdge(a, b, RelationshipRunsOn, "t", "acct", now)
	if e1.ID != e2.ID {
		t.Errorf("synthEdge: same args produced different IDs: %s vs %s", e1.ID, e2.ID)
	}
	// Different relationship type ⇒ different ID
	e3 := synthEdge(a, b, RelationshipManages, "t", "acct", now)
	if e1.ID == e3.ID {
		t.Errorf("synthEdge: RUNS_ON and MANAGES produced same ID %s", e1.ID)
	}
	// Edge source/level/active invariants
	if e1.Source != "k8s" || e1.Level != "Tenant" || !e1.IsActive {
		t.Errorf("synth edge invariants violated: source=%s level=%s active=%v", e1.Source, e1.Level, e1.IsActive)
	}
}

// TestUnmarshalLabels covers the labels-JSONB decode that happens once
// per synth row. The k8s_pods.labels column is sometimes written as a
// {string: string} map and sometimes as {string: any} when label values
// are non-string. Both shapes must reach the result as map[string]string.
func TestUnmarshalLabels(t *testing.T) {
	if got := unmarshalLabels(nil); len(got) != 0 {
		t.Errorf("nil input: got %v, want empty map", got)
	}
	if got := unmarshalLabels([]byte(`{}`)); len(got) != 0 {
		t.Errorf("empty JSON: got %v, want empty map", got)
	}
	if got := unmarshalLabels([]byte(`{"app":"api","version":"v1"}`)); got["app"] != "api" || got["version"] != "v1" {
		t.Errorf("string-string JSON not decoded: got %v", got)
	}
	// Mixed types (booleans, numbers) must stringify, mirroring NewNode's
	// label extraction in helpers.go.
	mixed := unmarshalLabels([]byte(`{"app":"api","scale":3,"ha":true}`))
	if mixed["app"] != "api" || mixed["scale"] != "3" || mixed["ha"] != "true" {
		t.Errorf("mixed-type JSON not stringified correctly: got %v", mixed)
	}
}
