package api

import (
	"testing"

	"nudgebee/services/security"
)

// TestRequiredPermission_FailClosed locks the relay authorization contract:
// only enumerated read action_names map to Read; everything else (mutations,
// exec, job-creating actions, and crucially anything not yet enumerated) must
// require write access so a read-only role can never invoke it.
func TestRequiredPermission_FailClosed(t *testing.T) {
	reads := []string{
		"get_resource",
		"get_resource_yaml",
		"metrics",
		"logs",
		"traces",
		"service_map",
		"pod_metric_enricher",
		"datadog_incident",
	}
	for _, a := range reads {
		if got := requiredPermission(a); got != security.SecurityAccessTypeRead {
			t.Errorf("requiredPermission(%q) = %q, want Read", a, got)
		}
	}

	// Known mutating relay actions (must require write) plus an unknown/typo
	// action that must fail closed rather than default to read.
	writes := []string{
		"replica_rightsizing",
		"replica_right_sizing",
		"rightsize_pvc",
		"rightsizing_resource",
		"volume_delete",
		"continuous_rightsizing",
		"delete_pod",
		"replace_workload",
		"kubectl_command_executor",
		"pod_bash_enricher",
		"nubi_enricher",
		"image_scanner",
		"pod_profiler",
		"",                        // empty action_name
		"some_unenumerated_write", // future action nobody added to readActions
	}
	for _, a := range writes {
		if got := requiredPermission(a); got != security.SecurityAccessTypeUpdate {
			t.Errorf("requiredPermission(%q) = %q, want Update (fail-closed)", a, got)
		}
	}
}

func TestBuildRelayKubectlLogs(t *testing.T) {
	build := func(params map[string]any) (string, error) {
		p := &relayProxyRequest{Body: map[string]any{"action_params": params}}
		err := buildRelayKubectlLogs("request", relayKubectlAction, p)
		cmd, _ := p.Body["action_params"].(map[string]any)["command"].(string)
		return cmd, err
	}

	cmd, err := build(map[string]any{"kind": "Deployment", "name": "api", "namespace": "shop"})
	if err != nil || cmd != "kubectl logs deployment/api -n shop --tail=1000 --all-containers=true" {
		t.Fatalf("got %q, %v", cmd, err)
	}
	cmd, err = build(map[string]any{"kind": "pod", "name": "api-1.x", "namespace": "shop", "container": "app", "tail": float64(50)})
	if err != nil || cmd != "kubectl logs pod/api-1.x -n shop --tail=50 -c app" {
		t.Fatalf("got %q, %v", cmd, err)
	}

	for _, bad := range []map[string]any{
		{"command": "kubectl get secrets -A -o yaml"},
		{"command": "kubectl logs deployment/api -n shop", "kind": "deployment", "name": "api", "namespace": "shop"},
		{"kind": "secret", "name": "db", "namespace": "shop"},
		{"kind": "deployment", "name": "api -n x get secrets", "namespace": "shop"},
		{"kind": "deployment", "name": "api", "namespace": "shop --raw=/api/v1/secrets"},
		{"kind": "deployment", "name": "api", "namespace": "shop", "container": "a b"},
		{"kind": "deployment", "name": "api", "namespace": "shop", "tail": float64(0)},
		{"kind": "deployment", "name": "api", "namespace": "shop", "tail": "1000;"},
		{"kind": "deployment", "name": "api", "namespace": "shop", "tail": float64(1.5)},
		{"kind": "deployment", "name": "", "namespace": "shop"},
		{},
	} {
		if _, err := build(bad); err == nil {
			t.Errorf("params %v: expected a refusal", bad)
		}
	}

	// Other actions and other proxy endpoints are left alone.
	p := &relayProxyRequest{Body: map[string]any{"action_params": map[string]any{"command": "x"}}}
	if err := buildRelayKubectlLogs("request", "get_resource", p); err != nil {
		t.Fatalf("get_resource must pass through: %v", err)
	}
	if err := buildRelayKubectlLogs("grafana", relayKubectlAction, p); err != nil {
		t.Fatalf("grafana must pass through: %v", err)
	}
	if p.Body["action_params"].(map[string]any)["command"] != "x" {
		t.Fatal("pass-through must not rewrite params")
	}
}

func TestRefuseRelaySecretResource(t *testing.T) {
	refused := func(actionName string, params map[string]any) bool {
		return refuseRelaySecretResource("request", actionName, &relayProxyRequest{Body: map[string]any{"action_params": params}}) != nil
	}
	for _, params := range []map[string]any{
		{"version": "v1", "resource_type": "secrets", "all_namespaces": true},
		{"version": "v1", "resource_type": "pods, Secrets"},
		{"kind": "Secret", "name": "db", "namespace": "prod"},
		{"group": "external-secrets.io", "version": "v1beta1", "resource_type": "externalsecrets"},
		{"resource_type": []any{"pods", "secrets"}},
	} {
		for _, action := range []string{"get_resource", "get_resource_yaml"} {
			if !refused(action, params) {
				t.Errorf("%s %v: expected a refusal", action, params)
			}
		}
	}
	for _, params := range []map[string]any{
		{"version": "v1", "resource_type": "persistentvolumeclaims"},
		{"version": "v1", "resource_type": "services,configmaps"},
		{"kind": "Deployment", "name": "secret-rotator", "namespace": "prod"},
	} {
		if refused("get_resource", params) {
			t.Errorf("%v: expected pass-through", params)
		}
	}
	if refused("prometheus_enricher", map[string]any{"resource_type": "secrets"}) {
		t.Error("other actions must pass through")
	}
}
