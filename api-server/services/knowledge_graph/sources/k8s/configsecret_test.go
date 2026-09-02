package k8s

import (
	"encoding/json"
	"reflect"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// unmarshalPodSpec parses a YAML-equivalent JSON pod spec the way the relay
// hands it over — generic map[string]interface{}, no typed K8s structs.
func unmarshalPodSpec(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal pod spec: %v", err)
	}
	return m
}

func newTestSource(t *testing.T) *K8sSource {
	t.Helper()
	src, err := NewK8sSource(K8sSourceConfig{}, nil)
	if err != nil {
		t.Fatalf("NewK8sSource: %v", err)
	}
	return src
}

// TestRefsFromPodSpec_SecretAsEnvVar is the reported scenario verbatim: a
// bare Pod consuming a Secret through env[].valueFrom.secretKeyRef. Before
// the Pod fetch was added this produced no USES_SECRET edge at all, so a
// security reviewer had no in-product way to see the reference.
func TestRefsFromPodSpec_SecretAsEnvVar(t *testing.T) {
	src := newTestSource(t)
	spec := unmarshalPodSpec(t, `{
		"restartPolicy": "Never",
		"containers": [{
			"name": "demo",
			"image": "busybox:1.36",
			"env": [{
				"name": "password",
				"valueFrom": {"secretKeyRef": {"name": "test-secret", "key": "password"}}
			}]
		}]
	}`)

	configs, secrets, refKinds := src.refsFromPodSpec(spec)

	if len(configs) != 0 {
		t.Errorf("configNames = %v, want empty", configs)
	}
	if !reflect.DeepEqual(secrets, []string{"test-secret"}) {
		t.Fatalf("secretNames = %v, want [test-secret]", secrets)
	}
	if got := refKinds["test-secret"]; !reflect.DeepEqual(got, []string{secretRefKindEnv}) {
		t.Errorf("refKinds[test-secret] = %v, want [%s]", got, secretRefKindEnv)
	}
}

func TestRefsFromPodSpec_RefKinds(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		secret   string
		wantKind []string
	}{
		{
			name:     "volume mount is the CIS-preferred form",
			spec:     `{"volumes": [{"name": "creds", "secret": {"secretName": "db-secret"}}]}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindVolume},
		},
		{
			name:     "projected volume source",
			spec:     `{"volumes": [{"name": "creds", "projected": {"sources": [{"secret": {"name": "db-secret"}}]}}]}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindVolume},
		},
		{
			name:     "envFrom injects every key at once",
			spec:     `{"containers": [{"name": "app", "envFrom": [{"secretRef": {"name": "db-secret"}}]}]}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindEnvFrom},
		},
		{
			name:     "init container counts",
			spec:     `{"initContainers": [{"name": "migrate", "env": [{"name": "PW", "valueFrom": {"secretKeyRef": {"name": "db-secret", "key": "pw"}}}]}]}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindEnv},
		},
		{
			name:     "snake_case relay shape",
			spec:     `{"containers": [{"name": "app", "env": [{"name": "PW", "value_from": {"secret_key_ref": {"name": "db-secret", "key": "pw"}}}]}]}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindEnv},
		},
		{
			name: "same secret consumed both ways reports both, sorted",
			spec: `{
				"volumes": [{"name": "creds", "secret": {"secretName": "db-secret"}}],
				"containers": [{"name": "app", "env": [{"name": "PW", "valueFrom": {"secretKeyRef": {"name": "db-secret", "key": "pw"}}}]}]
			}`,
			secret:   "db-secret",
			wantKind: []string{secretRefKindEnv, secretRefKindVolume},
		},
	}

	src := newTestSource(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, secrets, refKinds := src.refsFromPodSpec(unmarshalPodSpec(t, tt.spec))
			if !reflect.DeepEqual(secrets, []string{tt.secret}) {
				t.Fatalf("secretNames = %v, want [%s]", secrets, tt.secret)
			}
			if got := refKinds[tt.secret]; !reflect.DeepEqual(got, tt.wantKind) {
				t.Errorf("refKinds[%s] = %v, want %v", tt.secret, got, tt.wantKind)
			}
		})
	}
}

func TestExtractPodTemplateSpec(t *testing.T) {
	tests := []struct {
		name        string
		kind        string
		obj         string
		wantMarker  string
		wantMissing bool
	}{
		{
			name:       "bare Pod spec is already the pod spec",
			kind:       "Pod",
			obj:        `{"spec": {"marker": "pod"}}`,
			wantMarker: "pod",
		},
		{
			name:       "Deployment unwraps spec.template.spec",
			kind:       "Deployment",
			obj:        `{"spec": {"template": {"spec": {"marker": "deployment"}}}}`,
			wantMarker: "deployment",
		},
		{
			name:       "CronJob unwraps the extra jobTemplate layer",
			kind:       "CronJob",
			obj:        `{"spec": {"jobTemplate": {"spec": {"template": {"spec": {"marker": "cronjob"}}}}}}`,
			wantMarker: "cronjob",
		},
		{
			name:        "Pod without a spec",
			kind:        "Pod",
			obj:         `{"metadata": {"name": "x"}}`,
			wantMissing: true,
		},
		{
			// A Pod's spec must NOT be reached through the template path —
			// that would silently return nil for every bare pod.
			name:        "Deployment without a template",
			kind:        "Deployment",
			obj:         `{"spec": {"replicas": 1}}`,
			wantMissing: true,
		},
	}

	src := newTestSource(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(tt.obj), &obj); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := src.extractPodTemplateSpec(obj, tt.kind)
			if tt.wantMissing {
				if got != nil {
					t.Fatalf("extractPodTemplateSpec = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("extractPodTemplateSpec = nil, want marker %q", tt.wantMarker)
			}
			if got["marker"] != tt.wantMarker {
				t.Errorf("marker = %v, want %q", got["marker"], tt.wantMarker)
			}
		})
	}
}

// TestHasOwnerReferences guards the dedup rule: a pod created by a ReplicaSet
// already has its refs recorded against that ReplicaSet, so keeping it would
// duplicate every edge at pod level.
func TestHasOwnerReferences(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want bool
	}{
		{"bare pod has no owner", `{"name": "secret-env-demo"}`, false},
		{"empty list is not an owner", `{"ownerReferences": []}`, false},
		{"camelCase owner", `{"ownerReferences": [{"kind": "ReplicaSet", "name": "web-1"}]}`, true},
		{"snake_case owner", `{"owner_references": [{"kind": "ReplicaSet", "name": "web-1"}]}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var md map[string]interface{}
			if err := json.Unmarshal([]byte(tt.md), &md); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := hasOwnerReferences(md); got != tt.want {
				t.Errorf("hasOwnerReferences = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCreateWorkloadConfigSecretEdges_RefKinds pins the edge property that
// makes the graph answer "how is this secret consumed" without exposing the
// secret's value.
func TestCreateWorkloadConfigSecretEdges_RefKinds(t *testing.T) {
	src := newTestSource(t)
	req := &core.SourceBuildRequest{TenantID: "t1", CloudAccountID: "a1"}

	workloads := []K8sWorkloadRow{
		{Kind: "Pod", Namespace: "security-lab", Name: "secret-env-demo", ClusterName: "aks-1"},
		{Kind: "Deployment", Namespace: "security-lab", Name: "legacy", ClusterName: "aks-1"},
	}
	workloadNodes := map[string]*core.DbNode{
		"aks-1/Pod/security-lab/secret-env-demo": {ID: "pod-node"},
		"aks-1/Deployment/security-lab/legacy":   {ID: "deploy-node"},
	}
	secretByKey := map[string]*core.DbNode{
		"security-lab/test-secret": {ID: "secret-node"},
	}
	specRefs := map[string]workloadSpecRefs{
		workloadSpecRefsKey("Pod", "security-lab", "secret-env-demo"): {
			Secrets:        []string{"test-secret"},
			SecretRefKinds: map[string][]string{"test-secret": {secretRefKindEnv}},
		},
		// No SecretRefKinds — stands in for an edge built before this field
		// existed. ref_kinds must be omitted, not written empty.
		workloadSpecRefsKey("Deployment", "security-lab", "legacy"): {
			Secrets: []string{"test-secret"},
		},
	}

	edges := src.createWorkloadConfigSecretEdges(
		workloads, workloadNodes, map[string]*core.DbNode{}, secretByKey, specRefs, req)

	if len(edges) != 2 {
		t.Fatalf("got %d edges, want 2", len(edges))
	}

	bySource := map[string]*core.DbEdge{}
	for _, e := range edges {
		bySource[e.SourceNodeID] = e
	}

	podEdge, ok := bySource["pod-node"]
	if !ok {
		t.Fatal("no edge from the bare Pod — the pod-level USES_SECRET regression is back")
	}
	if podEdge.RelationshipType != core.RelationshipUsesSecret {
		t.Errorf("relationship = %v, want %v", podEdge.RelationshipType, core.RelationshipUsesSecret)
	}
	if got, want := podEdge.Properties["ref_kinds"], []string{secretRefKindEnv}; !reflect.DeepEqual(got, want) {
		t.Errorf("ref_kinds = %v, want %v", got, want)
	}
	if podEdge.Properties["secret_name"] != "test-secret" {
		t.Errorf("secret_name = %v, want test-secret", podEdge.Properties["secret_name"])
	}

	if _, present := bySource["deploy-node"].Properties["ref_kinds"]; present {
		t.Error("ref_kinds should be absent when the ref form is unknown")
	}
}

// TestRefsFromPodSpec_NeverReadsSecretValues is a standing guard: this walk
// reads workload specs only. A Secret's data never enters the graph, so a
// USES_SECRET edge is safe to show to anyone who can see the workload.
func TestRefsFromPodSpec_NeverReadsSecretValues(t *testing.T) {
	src := newTestSource(t)
	spec := unmarshalPodSpec(t, `{
		"containers": [{
			"name": "demo",
			"env": [{"name": "password", "valueFrom": {"secretKeyRef": {"name": "test-secret", "key": "password"}}}]
		}]
	}`)

	_, secrets, refKinds := src.refsFromPodSpec(spec)

	// Only the Secret's name and the consumption form travel onward — the
	// key name is deliberately not returned, and the value is never fetched.
	if !reflect.DeepEqual(secrets, []string{"test-secret"}) {
		t.Fatalf("secretNames = %v", secrets)
	}
	for name, kinds := range refKinds {
		for _, k := range kinds {
			if k != secretRefKindEnv && k != secretRefKindEnvFrom && k != secretRefKindVolume {
				t.Errorf("refKinds[%s] contains unexpected value %q", name, k)
			}
		}
	}
}
