package k8s

import (
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// TestManualNode_K8sKeyMatchesCollector: every declarable Kubernetes kind gets
// the node id the account build writes for the same workload (through the full
// convertWorkloadsToGraph pass), so a declared workload is adopted, not
// duplicated, when the cluster reports it. See the AWS test.
func TestManualNode_K8sKeyMatchesCollector(t *testing.T) {
	b, ok := core.ManualNodeBuilderFor("K8s")
	if !ok {
		t.Fatal("no K8s manual node builder registered")
	}
	declarable := b.Kinds()
	if len(declarable) != len(k8sManualKinds) {
		t.Fatalf("expected all %d declarable kinds, got %d", len(k8sManualKinds), len(declarable))
	}

	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}
	const tenant, account = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	req := &core.SourceBuildRequest{TenantID: tenant, CloudAccountID: account}

	for _, kind := range declarable {
		kind := kind
		t.Run(kind.SpecificType, func(t *testing.T) {
			row := K8sWorkloadRow{
				ID: "33333333-3333-3333-3333-333333333333", TenantID: tenant, CloudAccountID: account,
				Kind: kind.SpecificType[len("Kubernetes"):], Namespace: "payments", Name: "checkout",
				ClusterName: "prod-cluster", IsActive: true, Meta: []byte("{}"), Labels: []byte("{}"),
			}
			k8sNodeMap := map[string]*core.DbNode{}
			nodes, _, _, _, _ := src.convertWorkloadsToGraph([]K8sWorkloadRow{row}, &k8sNodeMap, map[string]string{}, req)
			var collected *core.DbNode
			for _, n := range nodes {
				if n.SpecificType == kind.SpecificType && n.Properties["name"] == "checkout" {
					collected = n
				}
			}
			if collected == nil {
				t.Fatalf("collector built no %s node", kind.SpecificType)
			}

			manual, err := b.Build(core.ManualNodeInput{
				TenantID: tenant, CloudAccountID: account, CloudProvider: "K8s", AccountName: "prod-cluster",
				SpecificType: kind.SpecificType, Name: "checkout", Namespace: "payments",
			})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if manual.UniqueKey != collected.UniqueKey || manual.ID != collected.ID || manual.NodeType != collected.NodeType {
				t.Errorf("identity drifted from the collector's\n manual:    %s %s (%s)\n collector: %s %s (%s)",
					manual.NodeType, manual.UniqueKey, manual.ID, collected.NodeType, collected.UniqueKey, collected.ID)
			}
			// The resolver matches namespace and cluster qualifiers on these.
			if manual.QueryAttributes["namespace"] != "payments" || manual.QueryAttributes["cluster"] != "prod-cluster" {
				t.Errorf("namespace/cluster not queryable: %v", manual.QueryAttributes)
			}
		})
	}

	// A Service is also built from the relay's live view of the cluster; a
	// declaration must merge with that node too.
	t.Run("KubernetesService via relay", func(t *testing.T) {
		svc := &K8sServiceFromRelay{Metadata: K8sServiceMetadata{Name: "checkout", Namespace: "payments"}}
		relayNode := src.createNodeFromK8sService(svc, "prod-cluster", req)
		manual, err := b.Build(core.ManualNodeInput{
			TenantID: tenant, CloudAccountID: account, CloudProvider: "K8s", AccountName: "prod-cluster",
			SpecificType: "KubernetesService", Name: "checkout", Namespace: "payments",
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if manual.ID != relayNode.ID || manual.UniqueKey != relayNode.UniqueKey {
			t.Errorf("identity drifted from the relay-built service\n manual: %s\n relay:  %s", manual.UniqueKey, relayNode.UniqueKey)
		}
	})

	for name, in := range map[string]core.ManualNodeInput{
		"namespace required":  {SpecificType: "KubernetesDeployment", Name: "checkout"},
		"pods not declarable": {SpecificType: "KubernetesPod", Name: "checkout", Namespace: "payments"},
		"no network":          {SpecificType: "KubernetesDeployment", Name: "checkout", Namespace: "payments", Network: &core.ManualNodeNetwork{ResourceID: "x"}},
	} {
		if _, err := b.Build(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
