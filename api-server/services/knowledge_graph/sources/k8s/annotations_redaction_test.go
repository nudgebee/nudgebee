package k8s

import (
	"encoding/json"
	"strings"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// The secret material kubectl apply leaves in last-applied-configuration.
const (
	secretValue    = "cG9zdGdyZXM6Ly91c2VyOmh1bnRlcjJAZGIvbmI=" // base64 DB URL
	envValue       = "super-secret-inline-env-value"
	lastAppliedKey = "kubectl.kubernetes.io/last-applied-configuration"
)

func lastApplied(kind string) string {
	return `{"apiVersion":"v1","kind":"` + kind + `","metadata":{"name":"nudgebee"},` +
		`"data":{"DATABASE_URL":"` + secretValue + `"},` +
		`"spec":{"template":{"spec":{"containers":[{"env":[{"name":"TOKEN","value":"` + envValue + `"}]}]}}}}`
}

func lastAppliedAnnotations(kind string) map[string]interface{} {
	return map[string]interface{}{
		lastAppliedKey:                    lastApplied(kind),
		"objectset.rio.cattle.io/applied": "H4sIAAAAAAAA/" + secretValue,
		"meta.helm.sh/release-name":       "nudgebee",
	}
}

// assertNoSecretMaterial fails if anything in the node — properties, query or
// ontology attributes, labels — carries the secret values or the annotation.
func assertNoSecretMaterial(t *testing.T, node *core.DbNode) {
	t.Helper()
	if node == nil {
		t.Fatal("node not built")
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, bad := range []string{secretValue, envValue, lastAppliedKey, "objectset.rio.cattle.io/applied"} {
		if strings.Contains(s, bad) {
			t.Fatalf("%s node carries %q:\n%s", node.NodeType, bad, s)
		}
	}
}

func testReq() *core.SourceBuildRequest {
	return &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}
}

func TestSecretNode_NoSecretMaterialFromAnnotations(t *testing.T) {
	src := newTestSource(t)
	ann := lastAppliedAnnotations("Secret")
	ann["cert-manager.io/common-name"] = "db.internal"
	ann["kubernetes.io/service-account.name"] = "builder"
	sec := &K8sSecretFromRelay{
		Metadata: K8sServiceMetadata{Name: "nudgebee", Namespace: "nudgebee", Annotations: ann},
		Type:     "Opaque",
		Data:     map[string]interface{}{"DATABASE_URL": nil},
	}
	node := src.createK8sSecretNode(sec, "c1", testReq())
	assertNoSecretMaterial(t, node)

	got, _ := node.Properties["annotations"].(map[string]any)
	want := map[string]any{
		"meta.helm.sh/release-name":          "nudgebee",
		"kubernetes.io/service-account.name": "builder",
		"cert-manager.io/common-name":        core.RedactedAnnotationValue,
	}
	if len(got) != len(want) {
		t.Fatalf("annotations = %v; want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("annotations[%q] = %v; want %v", k, got[k], v)
		}
	}
	// The caller's map is not modified.
	if _, ok := ann[lastAppliedKey]; !ok {
		t.Fatal("SanitizeAnnotations mutated its input")
	}
}

// A Secret whose only annotation is last-applied gets no annotations property.
func TestSecretNode_OnlyLastAppliedDropsProperty(t *testing.T) {
	src := newTestSource(t)
	sec := &K8sSecretFromRelay{Metadata: K8sServiceMetadata{
		Name: "s", Namespace: "n",
		Annotations: map[string]interface{}{lastAppliedKey: lastApplied("Secret")},
	}}
	node := src.createK8sSecretNode(sec, "c1", testReq())
	assertNoSecretMaterial(t, node)
	if _, ok := node.Properties["annotations"]; ok {
		t.Fatalf("annotations should be absent, got %v", node.Properties["annotations"])
	}
}

func TestOtherKinds_NoSecretMaterialFromAnnotations(t *testing.T) {
	src := newTestSource(t)
	req := testReq()
	meta := func(kind string) K8sServiceMetadata {
		return K8sServiceMetadata{Name: "x", Namespace: "n", Annotations: lastAppliedAnnotations(kind)}
	}

	cases := map[string]*core.DbNode{
		"ConfigMap":      src.createK8sConfigMapNode(&K8sConfigMapFromRelay{Metadata: meta("ConfigMap")}, "c1", req),
		"Service":        src.createNodeFromK8sService(&K8sServiceFromRelay{Metadata: meta("Service")}, "c1", req),
		"ServiceAccount": src.createK8sServiceAccountNode(&K8sServiceAccountFromRelay{Metadata: meta("ServiceAccount")}, "c1", req),
		"PV":             src.createNodeFromK8sPV(&K8sPVFromRelay{Metadata: meta("PersistentVolume")}, "c1", req),
		"PVC":            src.createNodeFromK8sPVC(&K8sPVCFromRelay{Metadata: meta("PersistentVolumeClaim")}, "c1", req),
		"NodePool": src.createKarpenterNodePoolNode(&K8sCRDFromRelay{Metadata: K8sCRDMetadata{
			Name: "np", Annotations: lastAppliedAnnotations("NodePool"),
		}}, "c1", req),
	}

	ing := &k8sIngressResource{}
	ing.Metadata.Name, ing.Metadata.Namespace = "ing", "n"
	ing.Metadata.Annotations = map[string]string{
		lastAppliedKey:                        lastApplied("Ingress"),
		"nginx.ingress.kubernetes.io/rewrite": "/",
	}
	p := src.newIngressBackendProcessor("a1", "t1", nil, nil, req)
	cases["Ingress"] = p.createIngressNode(ing, "c1", "")

	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			assertNoSecretMaterial(t, node)
			ann, _ := node.Properties["annotations"].(map[string]any)
			if len(ann) == 0 {
				t.Fatal("non-object annotations were dropped too")
			}
		})
	}
	// Non-Secret kinds keep ordinary annotation values.
	if v := cases["Ingress"].Properties["annotations"].(map[string]any)["nginx.ingress.kubernetes.io/rewrite"]; v != "/" {
		t.Fatalf("ingress annotation value = %v; want /", v)
	}
}

// Workloads read annotations from the k8s_workloads row (meta.config); a
// kubectl-applied Deployment's last-applied carries its inline env values.
func TestWorkloadNode_NoSecretMaterialFromAnnotations(t *testing.T) {
	src := newTestSource(t)
	config := map[string]interface{}{
		"labels":      map[string]interface{}{"app": "api"},
		"annotations": lastAppliedAnnotations("Deployment"),
	}
	row := workloadRow(t, "Deployment", "n", "api", config)
	k8sNodeMap := map[string]*core.DbNode{}
	nodes, _, _, _, _ := src.convertWorkloadsToGraph([]K8sWorkloadRow{row}, &k8sNodeMap, map[string]string{}, testReq())

	var found bool
	for _, n := range nodes {
		assertNoSecretMaterial(t, n)
		if n.NodeType == core.NodeTypeWorkload {
			found = true
			ann, _ := n.Properties["annotations"].(map[string]any)
			if ann["meta.helm.sh/release-name"] != "nudgebee" {
				t.Fatalf("workload annotations = %v", ann)
			}
		}
	}
	if !found {
		t.Fatal("no Workload node built")
	}
}

// workloadRow shapes a k8s_workloads row the way the collector writes it:
// meta.config is the agent's config, and the labels column is config.labels.
func workloadRow(t *testing.T, kind, namespace, name string, config map[string]interface{}) K8sWorkloadRow {
	t.Helper()
	meta, err := json.Marshal(map[string]interface{}{"config": config, "namespace": namespace})
	if err != nil {
		t.Fatal(err)
	}
	labels, err := json.Marshal(config["labels"])
	if err != nil {
		t.Fatal(err)
	}
	return K8sWorkloadRow{
		ID: kind + "/" + namespace + "/" + name, Kind: kind, Namespace: namespace, Name: name,
		ClusterName: "c1", IsActive: true, Meta: meta, Labels: labels,
	}
}
