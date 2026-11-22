package k8s

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// An Argo Rollout reached the inventory but never became a node: the kind was
// not declared, so it fell through to the generic Workload shape with a
// "KubernetesRollout" specific_type no schema and no ontology spec knew (#42134,
// 58 rows across 38 accounts producing 0 nodes).
//
// rolloutMeta is the real collector shape, copied from a k8s_workloads row on
// test (namespace rollout-test, Argo Rollouts installed on the dev cluster since
// 2026-07-15). Three details about it are load-bearing and easy to get wrong
// from the Kubernetes API docs alone:
//
//   - there is no "spec" object — the collector flattens the pod template into
//     meta.config, so extractControllerFields never runs and replicas /
//     strategy_type stay absent. That is true of Deployments on the same
//     account too, which is why these tests assert Rollout parity with a
//     Deployment rather than the presence of those two fields.
//   - "status" and "status_info" are null, so phase / ready_replicas /
//     observed_generation stay absent for the same reason.
//   - the revision annotation is Argo's "rollout.argoproj.io/revision", not
//     "deployment.kubernetes.io/revision", so deployment_revision stays absent.
const rolloutMeta = `{
	"node": null,
	"config": {
		"ip": "10.64.6.125",
		"labels": {"app": "canary-demo"},
		"annotations": {"rollout.argoproj.io/revision": "2"},
		"containers": [
			{"name": "web", "image": "nginx:1.27-alpine",
			 "resources": {"requests": {"cpu": "10m", "memory": "100Mi"},
			               "limits": {"memory": "100Mi"}}}
		],
		"service_account": ""
	},
	"status": null,
	"status_info": null,
	"namespace": "rollout-test",
	"ready_pods": 5,
	"total_pods": 5,
	"controllerKind": "Rollout",
	"is_helm_release": false
}`

func newRolloutTestSource(t *testing.T) *K8sSource {
	t.Helper()
	source, err := NewK8sSource(K8sSourceConfig{
		TenantID:       "test-tenant",
		CloudAccountID: "test-account",
	}, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err != nil {
		t.Fatalf("failed to create K8sSource: %v", err)
	}
	return source
}

func rolloutRow(kind, name string) K8sWorkloadRow {
	return K8sWorkloadRow{
		ID: "w-" + name, Kind: kind, Name: name, Namespace: "rollout-test",
		ClusterName: "c1", IsActive: true,
		Meta:   json.RawMessage(rolloutMeta),
		Labels: json.RawMessage(`{"app":"` + name + `"}`),
	}
}

// A Rollout row must build a Workload node labelled KubernetesRollout, with the
// query_attributes and ontology_attributes its schema and ontology spec
// declare. Zero of either means the specific_type reached core.NewNode with
// nothing registered for it — the original defect.
func TestCreateNodeFromWorkload_RolloutIsAFirstClassWorkload(t *testing.T) {
	source := newRolloutTestSource(t)
	req := &core.SourceBuildRequest{TenantID: "test-tenant", CloudAccountID: "test-account"}

	row := rolloutRow("Rollout", "canary-demo")
	node := source.createNodeFromWorkload(&row, map[string]string{}, req)

	if node.NodeType != core.NodeTypeWorkload {
		t.Errorf("node_type = %q, want %q: a Rollout owns replicas and pods, so it must stay in the Workload filters and in blast radius",
			node.NodeType, core.NodeTypeWorkload)
	}
	if node.SpecificType != "KubernetesRollout" {
		t.Errorf("specific_type = %q, want KubernetesRollout", node.SpecificType)
	}
	if len(node.QueryAttributes) == 0 {
		t.Error("query_attributes is empty — the KubernetesRollout schema is not registered, so no filter can find this node")
	}
	if len(node.OntologyAttributes) == 0 {
		t.Error("ontology_attributes is empty — KubernetesRollout has no entry in core/ontology_data_k8s.go, so the LLM agents cannot read this node")
	}
	if got := node.OntologyAttributes["type"]; got != "Rollout" {
		t.Errorf("ontology type = %v, want Rollout (it reads properties[subtype], the K8s Kind)", got)
	}
}

// The strong assertion: given identical collector metadata, a Rollout node
// carries exactly the attribute keys a Deployment node does. This is what makes
// a Rollout as useful to filters, blast radius and the agents as any other
// controller — and it fails on a schema or ontology spec that drifts from the
// apps/v1 Kinds, which sharing k8sWorkloadProperties / k8sWorkloadFields is
// meant to prevent.
func TestCreateNodeFromWorkload_RolloutMatchesDeploymentShape(t *testing.T) {
	source := newRolloutTestSource(t)
	req := &core.SourceBuildRequest{TenantID: "test-tenant", CloudAccountID: "test-account"}

	rows := []K8sWorkloadRow{rolloutRow("Rollout", "canary-demo"), rolloutRow("Deployment", "canary-demo")}
	rollout := source.createNodeFromWorkload(&rows[0], map[string]string{}, req)
	deployment := source.createNodeFromWorkload(&rows[1], map[string]string{}, req)

	keys := func(m map[string]interface{}) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	same := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	if q, want := keys(rollout.QueryAttributes), keys(deployment.QueryAttributes); !same(q, want) {
		t.Errorf("query_attributes keys = %v, want the Deployment's %v", q, want)
	}
	if o, want := keys(rollout.OntologyAttributes), keys(deployment.OntologyAttributes); !same(o, want) {
		t.Errorf("ontology_attributes keys = %v, want the Deployment's %v", o, want)
	}
}

// The account build only queries k8s_workloads for the kinds the registry
// declares as inventory, and the change detector only watches the ones that do
// not churn. A Rollout that is absent from either is the defect again in a
// different place: no rows read, or rows read but a deleted Rollout never
// noticed.
func TestRolloutIsQueriedAndWatched(t *testing.T) {
	kinds := defaultWorkloadKinds()
	found := false
	for _, k := range kinds {
		if k == "Rollout" {
			found = true
		}
	}
	if !found {
		t.Errorf("defaultWorkloadKinds() = %v, want it to include Rollout — the account build would not read those rows", kinds)
	}

	if got := detectedWorkloadKinds([]string{"Rollout"}); len(got) != 1 || got[0] != "Rollout" {
		t.Errorf("detectedWorkloadKinds([Rollout]) = %v, want [Rollout]: a Rollout does not churn, so the detector must watch it", got)
	}

	if declared := workloadKindFor("Rollout"); !declared.Patchable {
		t.Error("Rollout is not Patchable — a single Rollout's change would rebuild its whole account")
	}
}

// Rollout is declared Patchable, which claims a single row rebuilds the same
// node and the same own-edges the account build would. test/e2e_resource_patch_drift_test.go
// makes that claim for every kind in its fixture — but it iterates the fixture,
// not the registry, and no fixture carries a Rollout, so it passes without
// covering this kind. This is that comparison, scoped to a Rollout.
func TestRolloutPatchMatchesFullBuild(t *testing.T) {
	source := newRolloutTestSource(t)
	req := &core.SourceBuildRequest{TenantID: "test-tenant", CloudAccountID: "test-account"}

	rows := []K8sWorkloadRow{rolloutRow("Rollout", "canary-demo")}
	k8sNodeMap := map[string]*core.DbNode{}
	fullNodes, fullEdges, _, _, _ := source.convertWorkloadsToGraph(rows, &k8sNodeMap, map[string]string{}, req)
	patch := source.buildWorkloadPatch(&rows[0], map[string]string{}, req)

	if !patch.Patchable {
		t.Fatal("buildWorkloadPatch reports the row as not patchable")
	}

	byID := make(map[string]*core.DbNode, len(fullNodes))
	for _, n := range fullNodes {
		byID[n.ID] = n
	}
	full, ok := byID[patch.Node.ID]
	if !ok {
		t.Fatalf("patch node id %s is not one the full build produced — a patch would write a second node beside it", patch.Node.ID)
	}
	if full.NodeType != patch.Node.NodeType || full.SpecificType != patch.Node.SpecificType {
		t.Errorf("type drift: full build %s/%s, patch %s/%s",
			full.NodeType, full.SpecificType, patch.Node.NodeType, patch.Node.SpecificType)
	}
	if _, ok := byID[patch.Namespace.ID]; !ok {
		t.Error("patch namespace node id is not one the full build produced")
	}

	// Compare only the edges the signature claims, and leave USES_IMAGE out:
	// the patch runs container-image synthesis on its one node while
	// convertWorkloadsToGraph does not, exactly as the drift test allows for.
	keys := func(edges []*core.DbEdge) []string {
		out := []string{}
		for _, e := range edges {
			if e.SourceNodeID != full.ID || e.RelationshipType == core.RelationshipUsesImage {
				continue
			}
			for _, k := range workloadEdgeSignature.Kinds {
				ct, _ := e.Properties["connection_type"].(string)
				if e.RelationshipType == k.Relationship && (k.ConnectionType == "" || k.ConnectionType == ct) {
					out = append(out, string(e.RelationshipType)+"/"+ct+"->"+e.DestinationNodeID)
					break
				}
			}
		}
		sort.Strings(out)
		return out
	}

	want, got := keys(fullEdges), keys(patch.Edges)
	if len(want) == 0 {
		t.Fatal("full build emitted no signature edges for the Rollout — the comparison would be vacuous")
	}
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Errorf("edge drift for a Rollout\n full build: %v\n patch:      %v", want, got)
	}
}
