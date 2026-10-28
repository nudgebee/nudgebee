package k8s

import (
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"testing"

	"nudgebee/services/knowledge_graph/core"
)

// agentWorkloadConfigFile is a copy of the workload `config` the agent emits
// (k8s-agent runner/pkg/discovery/testdata/workload_config_contract.json, pinned
// by TestTemplateLabelsContract there). Rows are built from it so these tests
// break if the agent's key ever moves.
const agentWorkloadConfigFile = "testdata/agent_workload_config.json"

func agentWorkloadConfigs(t *testing.T) map[string]map[string]interface{} {
	t.Helper()
	raw, err := os.ReadFile(agentWorkloadConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var configs map[string]map[string]interface{}
	if err := json.Unmarshal(raw, &configs); err != nil {
		t.Fatal(err)
	}
	return configs
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

// withConfig returns a copy of config with key set to value, or removed when
// value is nil (an older agent that never sent the key).
func withConfig(config map[string]interface{}, key string, value interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(config))
	for k, v := range config {
		out[k] = v
	}
	if value == nil {
		delete(out, key)
	} else {
		out[key] = value
	}
	return out
}

func relayService(namespace, name string, selector map[string]interface{}) K8sServiceFromRelay {
	return K8sServiceFromRelay{
		Metadata: K8sServiceMetadata{Name: name, Namespace: namespace},
		Spec:     K8sServiceSpec{Selector: selector},
	}
}

// exposed lists the "<kind>/<name> matched_on" of every EXPOSES edge into the
// named Service.
func exposed(t *testing.T, workloads []K8sWorkloadRow, services []K8sServiceFromRelay, serviceName string) []string {
	t.Helper()
	source, err := NewK8sSource(K8sSourceConfig{TenantID: "test-tenant", CloudAccountID: "test-account"},
		slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatal(err)
	}
	req := &core.SourceBuildRequest{TenantID: "test-tenant", CloudAccountID: "test-account"}
	k8sNodeMap := make(map[string]*core.DbNode)
	workloadGraphNodes, _, clusters, namespaces, workloadNodes := source.convertWorkloadsToGraph(workloads, &k8sNodeMap, map[string]string{}, req)
	serviceNodes, edges, _, _ := source.convertK8sServicesToGraph(services, workloads, clusters, namespaces, workloadNodes, req)

	byID := map[string]*core.DbNode{}
	for _, n := range append(workloadGraphNodes, serviceNodes...) {
		byID[n.ID] = n
	}
	var got []string
	for _, e := range edges {
		if e.RelationshipType != core.RelationshipExposes {
			continue
		}
		src, dst := byID[e.SourceNodeID], byID[e.DestinationNodeID]
		if src == nil || dst == nil {
			t.Fatalf("EXPOSES edge with an unknown endpoint: %+v", e)
		}
		if dst.NodeType != core.NodeTypeK8sService {
			t.Fatalf("EXPOSES must point Workload → K8sService; destination is %s", dst.NodeType)
		}
		if dst.Properties["name"] != serviceName {
			continue
		}
		got = append(got, src.Properties["kind"].(string)+"/"+src.Properties["name"].(string)+" "+e.Properties["matched_on"].(string))
	}
	sort.Strings(got)
	return got
}

func assertExposed(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("EXPOSES = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EXPOSES = %v; want %v", got, want)
		}
	}
}

// A chart's primary database: `role: primary` is only on the pod template, and
// the StatefulSet is scaled to zero. Kubernetes would route the Service to its pods.
func TestServiceExposes_TemplateOnlyLabel(t *testing.T) {
	cfg := agentWorkloadConfigs(t)["StatefulSet"]
	workloads := []K8sWorkloadRow{workloadRow(t, "StatefulSet", "db", "db-postgresql", cfg)}
	services := []K8sServiceFromRelay{relayService("db", "db-postgresql", map[string]interface{}{
		"role": "primary", "app.kubernetes.io/name": "postgresql",
	})}

	assertExposed(t, exposed(t, workloads, services, "db-postgresql"), "StatefulSet/db-postgresql template_labels")
}

// An Argo Rollout with no labels of its own; its active Service's selector
// carries the hash Argo adds, which the pod template never has.
func TestServiceExposes_RolloutDropsArgoHash(t *testing.T) {
	cfg := agentWorkloadConfigs(t)["Rollout"]
	workloads := []K8sWorkloadRow{workloadRow(t, "Rollout", "shop", "checkout", cfg)}
	services := []K8sServiceFromRelay{relayService("shop", "checkout-active", map[string]interface{}{
		"app": "checkout", rolloutsPodTemplateHashKey: "555c944ddf",
	})}

	assertExposed(t, exposed(t, workloads, services, "checkout-active"), "Rollout/checkout template_labels")
}

// The Argo hash says the Service targets a Rollout's pods, so it is dropped only
// for Rollouts: a Deployment carrying the same labels (the Deployment a
// workloadRef Rollout points at, which Argo scales to zero) must not be linked.
func TestServiceExposes_ArgoHashNotDroppedForOtherKinds(t *testing.T) {
	deploymentCfg := withConfig(agentWorkloadConfigs(t)["Rollout"], templateLabelsKey, map[string]interface{}{"app": "checkout"})
	// A workloadRef Rollout: the agent sends an empty template.
	workloadRefCfg := withConfig(agentWorkloadConfigs(t)["Rollout"], templateLabelsKey, map[string]interface{}{})
	workloads := []K8sWorkloadRow{
		workloadRow(t, "Deployment", "shop", "checkout", deploymentCfg),
		workloadRow(t, "Rollout", "shop", "checkout-rollout", workloadRefCfg),
	}
	services := []K8sServiceFromRelay{relayService("shop", "checkout-active", map[string]interface{}{
		"app": "checkout", rolloutsPodTemplateHashKey: "555c944ddf",
	})}

	assertExposed(t, exposed(t, workloads, services, "checkout-active"))
}

// When the agent sent the template labels they decide: a Service that matches
// only the object's own labels does not route to the workload's pods.
func TestServiceExposes_TemplateLabelsTakePrecedence(t *testing.T) {
	cfg := withConfig(agentWorkloadConfigs(t)["StatefulSet"], "labels", map[string]interface{}{
		"app.kubernetes.io/name": "postgresql", "tier": "db",
	})
	workloads := []K8sWorkloadRow{workloadRow(t, "StatefulSet", "db", "db-postgresql", cfg)}
	services := []K8sServiceFromRelay{relayService("db", "db-tier", map[string]interface{}{"tier": "db"})}

	assertExposed(t, exposed(t, workloads, services, "db-tier"))
}

// A row from an agent that predates template_labels keeps today's behaviour:
// it is matched on the object's own labels.
func TestServiceExposes_OlderAgentFallsBackToObjectLabels(t *testing.T) {
	cfg := withConfig(agentWorkloadConfigs(t)["StatefulSet"], templateLabelsKey, nil)
	workloads := []K8sWorkloadRow{workloadRow(t, "StatefulSet", "db", "db-postgresql", cfg)}
	services := []K8sServiceFromRelay{
		relayService("db", "by-object-label", map[string]interface{}{"app.kubernetes.io/name": "postgresql"}),
		relayService("db", "by-template-label", map[string]interface{}{"role": "primary"}),
	}

	assertExposed(t, exposed(t, workloads, services, "by-object-label"), "StatefulSet/db-postgresql labels")
	assertExposed(t, exposed(t, workloads, services, "by-template-label"))
}

// An older agent's Rollout is matched as today, with the full selector: the Argo
// hash is dropped only for a match on template labels.
func TestServiceExposes_OlderAgentRolloutKeepsArgoHash(t *testing.T) {
	cfg := withConfig(agentWorkloadConfigs(t)["Rollout"], templateLabelsKey, nil)
	cfg = withConfig(cfg, "labels", map[string]interface{}{"app": "checkout"})
	workloads := []K8sWorkloadRow{workloadRow(t, "Rollout", "shop", "checkout", cfg)}
	services := []K8sServiceFromRelay{relayService("shop", "checkout-active", map[string]interface{}{
		"app": "checkout", rolloutsPodTemplateHashKey: "555c944ddf",
	})}

	assertExposed(t, exposed(t, workloads, services, "checkout-active"))
}

func TestServiceExposes_NoMatch(t *testing.T) {
	cfg := agentWorkloadConfigs(t)["StatefulSet"]
	workloads := []K8sWorkloadRow{workloadRow(t, "StatefulSet", "db", "db-postgresql", cfg)}
	rollout := []K8sWorkloadRow{workloadRow(t, "Rollout", "shop", "checkout", agentWorkloadConfigs(t)["Rollout"])}

	tests := []struct {
		name      string
		workloads []K8sWorkloadRow
		service   K8sServiceFromRelay
	}{
		{"other namespace", workloads, relayService("other", "s", map[string]interface{}{"role": "primary"})},
		{"value differs", workloads, relayService("db", "s", map[string]interface{}{"role": "replica"})},
		{"no selector", workloads, relayService("db", "s", nil)},
		{"only the Argo hash", rollout, relayService("shop", "s", map[string]interface{}{rolloutsPodTemplateHashKey: "555c944ddf"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertExposed(t, exposed(t, tt.workloads, []K8sServiceFromRelay{tt.service}, "s"))
		})
	}
}
