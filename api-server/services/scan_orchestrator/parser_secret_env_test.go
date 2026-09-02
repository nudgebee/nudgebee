package scan_orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

// secretEnvPod builds a pod whose single container sources `envVar` from
// secret/key. owner is an optional {kind, name} ownerReference.
func secretEnvPod(ns, name, container, envVar, secret, key string, owner ...string) map[string]any {
	md := map[string]any{"namespace": ns, "name": name}
	if len(owner) == 2 {
		md["owner_references"] = []any{
			map[string]any{"kind": owner[0], "name": owner[1]},
		}
	}
	return map[string]any{
		"metadata": md,
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name": container,
					"env": []any{
						map[string]any{
							"name": envVar,
							"value_from": map[string]any{
								"secret_key_ref": map[string]any{"name": secret, "key": key},
							},
						},
					},
				},
			},
		},
	}
}

func makeReplicaSet(ns, name, ownerKind, ownerName string) map[string]any {
	md := map[string]any{"namespace": ns, "name": name}
	if ownerName != "" {
		md["owner_references"] = []any{
			map[string]any{"kind": ownerKind, "name": ownerName},
		}
	}
	return map[string]any{"metadata": md}
}

// The customer scenario from the original security test: an ownerless pod with
// a secretKeyRef env var. This is the case the Knowledge Graph misses entirely.
func TestIdentifySecretEnvExposures_BarePod(t *testing.T) {
	pods := []map[string]any{
		secretEnvPod("security-lab", "secret-env-demo", "demo", "password", "test-secret", "password"),
	}

	got := IdentifySecretEnvExposures(pods, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if got[0].Kind != "Pod" || got[0].Name != "secret-env-demo" || got[0].Namespace != "security-lab" {
		t.Fatalf("unexpected owner: %+v", got[0])
	}
	if len(got[0].Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(got[0].Findings))
	}
	msg, _ := got[0].Findings[0]["message"].(string)
	for _, want := range []string{`"password"`, `"test-secret"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %s", msg, want)
		}
	}
	if c, _ := got[0].Findings[0]["container"].(string); c != "demo" {
		t.Errorf("container = %q, want demo", c)
	}
}

// Replicas of one Deployment must collapse to a single row anchored on the
// Deployment — not three rows anchored on churning pod names.
func TestIdentifySecretEnvExposures_RollsUpToDeployment(t *testing.T) {
	pods := []map[string]any{
		secretEnvPod("prod", "api-7d9f8b6c4-aaa", "api", "DB_PASSWORD", "db-creds", "password", "ReplicaSet", "api-7d9f8b6c4"),
		secretEnvPod("prod", "api-7d9f8b6c4-bbb", "api", "DB_PASSWORD", "db-creds", "password", "ReplicaSet", "api-7d9f8b6c4"),
		secretEnvPod("prod", "api-7d9f8b6c4-ccc", "api", "DB_PASSWORD", "db-creds", "password", "ReplicaSet", "api-7d9f8b6c4"),
	}
	rs := []map[string]any{makeReplicaSet("prod", "api-7d9f8b6c4", "Deployment", "api")}

	got := IdentifySecretEnvExposures(pods, rs)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if got[0].Kind != "Deployment" || got[0].Name != "api" {
		t.Fatalf("expected rollup to Deployment/api, got %s/%s", got[0].Kind, got[0].Name)
	}
	if len(got[0].Findings) != 1 {
		t.Fatalf("expected replicas to dedupe to 1 finding, got %d", len(got[0].Findings))
	}
}

// Without the ReplicaSet list the scan still reports, anchored one level lower.
func TestIdentifySecretEnvExposures_FallsBackToReplicaSet(t *testing.T) {
	pods := []map[string]any{
		secretEnvPod("prod", "api-7d9f8b6c4-aaa", "api", "DB_PASSWORD", "db-creds", "password", "ReplicaSet", "api-7d9f8b6c4"),
	}

	got := IdentifySecretEnvExposures(pods, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if got[0].Kind != "ReplicaSet" || got[0].Name != "api-7d9f8b6c4" {
		t.Fatalf("expected ReplicaSet/api-7d9f8b6c4, got %s/%s", got[0].Kind, got[0].Name)
	}
}

func TestIdentifySecretEnvExposures_EnvFromAndInitContainers(t *testing.T) {
	pods := []map[string]any{
		{
			"metadata": map[string]any{"namespace": "prod", "name": "worker"},
			"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"name":     "worker",
						"env_from": []any{map[string]any{"secret_ref": map[string]any{"name": "bulk-creds"}}},
					},
				},
				"init_containers": []any{
					map[string]any{
						"name": "migrate",
						"env": []any{map[string]any{
							"name":       "MIGRATION_TOKEN",
							"value_from": map[string]any{"secret_key_ref": map[string]any{"name": "migrate-secret", "key": "token"}},
						}},
					},
				},
			},
		},
	}

	got := IdentifySecretEnvExposures(pods, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if len(got[0].Findings) != 2 {
		t.Fatalf("expected envFrom + initContainer findings, got %d", len(got[0].Findings))
	}
	containers := map[string]bool{}
	for _, f := range got[0].Findings {
		c, _ := f["container"].(string)
		containers[c] = true
	}
	if !containers["worker"] || !containers["migrate"] {
		t.Errorf("expected both worker and migrate containers, got %v", containers)
	}
}

// Older agent builds send camelCase; both shapes must resolve.
func TestIdentifySecretEnvExposures_CamelCaseAgent(t *testing.T) {
	pods := []map[string]any{
		{
			"metadata": map[string]any{
				"namespace":       "prod",
				"name":            "legacy-abc",
				"ownerReferences": []any{map[string]any{"kind": "StatefulSet", "name": "legacy"}},
			},
			"spec": map[string]any{
				"containers": []any{
					map[string]any{
						"name": "app",
						"env": []any{map[string]any{
							"name":      "API_KEY",
							"valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "api-secret", "key": "key"}},
						}},
					},
				},
			},
		},
	}

	got := IdentifySecretEnvExposures(pods, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if got[0].Kind != "StatefulSet" || got[0].Name != "legacy" {
		t.Fatalf("expected StatefulSet/legacy, got %s/%s", got[0].Kind, got[0].Name)
	}
}

// A pod can carry several ownerReferences; only the controller=true one names
// the workload it belongs to.
func TestIdentifySecretEnvExposures_PrefersControllerOwnerRef(t *testing.T) {
	pod := secretEnvPod("prod", "api-7d9f8b6c4-aaa", "api", "DB_PASSWORD", "db-creds", "password")
	pod["metadata"].(map[string]any)["owner_references"] = []any{
		map[string]any{"kind": "BackupSchedule", "name": "nightly-backup"},
		map[string]any{"kind": "ReplicaSet", "name": "api-7d9f8b6c4", "controller": true},
	}
	rs := []map[string]any{makeReplicaSet("prod", "api-7d9f8b6c4", "Deployment", "api")}

	got := IdentifySecretEnvExposures([]map[string]any{pod}, rs)
	if len(got) != 1 {
		t.Fatalf("expected 1 exposure, got %d", len(got))
	}
	if got[0].Kind != "Deployment" || got[0].Name != "api" {
		t.Fatalf("expected rollup via the controller ref to Deployment/api, got %s/%s", got[0].Kind, got[0].Name)
	}

	// Nothing marked as controller — fall back to the first named ref.
	pod2 := secretEnvPod("prod", "orphan", "api", "DB_PASSWORD", "db-creds", "password")
	pod2["metadata"].(map[string]any)["owner_references"] = []any{
		map[string]any{"kind": "StatefulSet", "name": "legacy"},
	}
	got2 := IdentifySecretEnvExposures([]map[string]any{pod2}, nil)
	if len(got2) != 1 || got2[0].Kind != "StatefulSet" || got2[0].Name != "legacy" {
		t.Fatalf("expected fallback to StatefulSet/legacy, got %+v", got2)
	}
}

// A finished pod has no running container, so its env var is no longer live.
func TestIdentifySecretEnvExposures_SkipsTerminalPods(t *testing.T) {
	for _, phase := range []string{"Succeeded", "Failed"} {
		pod := secretEnvPod("security-lab", "secret-env-demo", "demo", "password", "test-secret", "password")
		pod["status"] = map[string]any{"phase": phase}
		if got := IdentifySecretEnvExposures([]map[string]any{pod}, nil); len(got) != 0 {
			t.Errorf("phase %s: expected no exposure, got %+v", phase, got)
		}
	}
	for _, phase := range []string{"Running", "Pending"} {
		pod := secretEnvPod("security-lab", "secret-env-demo", "demo", "password", "test-secret", "password")
		pod["status"] = map[string]any{"phase": phase}
		if got := IdentifySecretEnvExposures([]map[string]any{pod}, nil); len(got) != 1 {
			t.Errorf("phase %s: expected 1 exposure, got %d", phase, len(got))
		}
	}
	// No status yet — report rather than silently skip.
	pod := secretEnvPod("security-lab", "secret-env-demo", "demo", "password", "test-secret", "password")
	if got := IdentifySecretEnvExposures([]map[string]any{pod}, nil); len(got) != 1 {
		t.Errorf("statusless pod: expected 1 exposure, got %d", len(got))
	}
}

// A Secret mounted as a file is the remediation, not the finding.
func TestIdentifySecretEnvExposures_IgnoresVolumeMounts(t *testing.T) {
	pods := []map[string]any{
		{
			"metadata": map[string]any{"namespace": "prod", "name": "mounted"},
			"spec": map[string]any{
				"volumes":    []any{map[string]any{"secret": map[string]any{"secret_name": "file-secret"}}},
				"containers": []any{map[string]any{"name": "app"}},
			},
		},
	}

	if got := IdentifySecretEnvExposures(pods, nil); len(got) != 0 {
		t.Fatalf("expected no exposure for volume-mounted Secret, got %+v", got)
	}
}

func TestParseSecretEnvExposures_RowShape(t *testing.T) {
	pods := []map[string]any{
		secretEnvPod("security-lab", "secret-env-demo", "demo", "password", "test-secret", "password"),
	}
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}

	recs, err := ParseSecretEnvExposures(IdentifySecretEnvExposures(pods, nil), account)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	r := recs[0]
	if r.Category != "Configuration" {
		t.Errorf("category = %q, want Configuration", r.Category)
	}
	if r.RuleName != SecretEnvExposureRuleName {
		t.Errorf("rule_name = %q, want %q", r.RuleName, SecretEnvExposureRuleName)
	}
	if r.Severity != "Medium" {
		t.Errorf("severity = %q, want Medium", r.Severity)
	}
	if r.AccountObjectID != "security-lab/Pod/secret-env-demo" {
		t.Errorf("account_object_id = %q", r.AccountObjectID)
	}

	// The UI's buildRow reads an array of {namespace, kind, name, ...}.
	var payload []map[string]any
	if err := json.Unmarshal([]byte(r.Recommendation), &payload); err != nil {
		t.Fatalf("recommendation payload is not a JSON array: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 payload entry, got %d", len(payload))
	}
	for _, k := range []string{"namespace", "kind", "name", "container", "message"} {
		if _, ok := payload[0][k]; !ok {
			t.Errorf("payload missing %q, UI column would be blank", k)
		}
	}
	// Secret values must never appear anywhere in the row.
	if strings.Contains(r.Recommendation, "value") && !strings.Contains(r.Recommendation, "value_from") {
		t.Errorf("payload appears to carry a Secret value: %s", r.Recommendation)
	}
}

// A re-scan over unchanged cluster state must produce byte-identical payloads,
// otherwise the daily UPSERT rewrites every row and churns updated_at.
func TestIdentifySecretEnvExposures_DeterministicOrdering(t *testing.T) {
	pods := []map[string]any{
		secretEnvPod("prod", "multi", "a", "TOKEN_B", "s2", "b"),
	}
	pods[0]["spec"].(map[string]any)["containers"] = append(
		pods[0]["spec"].(map[string]any)["containers"].([]any),
		map[string]any{
			"name": "b",
			"env": []any{map[string]any{
				"name":       "TOKEN_A",
				"value_from": map[string]any{"secret_key_ref": map[string]any{"name": "s1", "key": "a"}},
			}},
		},
	)
	account := ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"}

	var first string
	for i := 0; i < 20; i++ {
		recs, err := ParseSecretEnvExposures(IdentifySecretEnvExposures(pods, nil), account)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(recs) != 1 {
			t.Fatalf("expected 1 recommendation, got %d", len(recs))
		}
		if i == 0 {
			first = recs[0].Recommendation
			continue
		}
		if recs[0].Recommendation != first {
			t.Fatalf("payload not deterministic:\n%s\n%s", first, recs[0].Recommendation)
		}
	}
}
