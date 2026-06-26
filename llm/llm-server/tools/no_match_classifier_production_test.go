package tools

import (
	"testing"

	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
)

// TestProductionFailingCommands_AreReclassified pins the actual
// production-failing kubectl/helm pipeline-grep commands from the last
// 48h of error data against isNoMatchExit. These same commands now
// flow through the wiring added at tool_kubectl.go and tool_helm.go to
// emit Success+no_matches:true instead of opaque exit-1 failures.
//
// Lives separately from TestIsNoMatchExit because the cases here are
// production-validated samples (a regression guard if real-world LLM
// usage shifts to new pipeline shapes), whereas TestIsNoMatchExit
// covers the helper's algorithmic correctness across synthetic edges.
func TestProductionFailingCommands_AreReclassified(t *testing.T) {
	exitOne := &workspace.CommandFailure{Status: "failed", StdErr: "exit status 1"}

	cases := []struct {
		name    string
		command string
		// wantReclassify is what the LLM SHOULD see after the fix is
		// wired into kubectl_execute / helm_execute.
		wantReclassify bool
	}{
		// kubectl + grep — the dominant pattern (4 of 5 Class-1 prod rows)
		{
			name:           "kubectl get ingress -A | grep -i nudgebee-api",
			command:        "kubectl get ingress -A | grep -i nudgebee-api",
			wantReclassify: true,
		},
		{
			name:           `kubectl get all -A --no-headers | grep -i -E "(redis-staging|db-staging)"`,
			command:        `kubectl get all -A --no-headers | grep -i -E "(redis-staging|db-staging)"`,
			wantReclassify: true,
		},
		{
			name:           "kubectl get svc -n nudgebee | grep api",
			command:        "kubectl get svc -n nudgebee | grep api",
			wantReclassify: true,
		},
		{
			name:           "kubectl logs <pod> --tail 50 | grep -i -E error pattern",
			command:        `kubectl logs backend-service-5ddcb45f47-fxr9g -n namespace-71 --tail 50 | grep -i -E "(error|exception|fatal|panic|fail|warn)"`,
			wantReclassify: true,
		},
		{
			name:           "kubectl get pods --all-namespaces 2>/dev/null | grep -i api-server",
			command:        "kubectl get pods --all-namespaces 2>/dev/null | grep -i api-server",
			wantReclassify: true,
		},
		{
			name:           "kubectl get secrets -A | grep -i nb-code-agent",
			command:        "kubectl get secrets -A | grep -i nb-code-agent",
			wantReclassify: true,
		},
		{
			name:           "kubectl get all -A | grep -i nb-code-agent",
			command:        "kubectl get all -A | grep -i nb-code-agent",
			wantReclassify: true,
		},
		// helm + grep
		{
			name:           "helm list -A -a | grep -i nb-code-agent",
			command:        "helm list -A -a | grep -i nb-code-agent",
			wantReclassify: true,
		},
		// kubectl logs with deployment selector + grep (Class 2a)
		{
			name:           `kubectl logs deployment/X | grep -i -E "redis config|validating redis config"`,
			command:        `kubectl logs deployment/services-server -n nudgebee --since-time=2026-06-13T09:45:00Z | grep -i -E "redis config|validating redis config"`,
			wantReclassify: true,
		},
		// kubectl get + grep with -n namespace (Class 3 family)
		{
			name:           "kubectl get svc -n app-108 | grep -i api",
			command:        "kubectl get svc -n app-108 | grep -i api",
			wantReclassify: true,
		},
		// Negative guards — these should NOT be reclassified
		{
			name:           "kubectl exec into pod (negative — no grep tail)",
			command:        "kubectl exec api-service-5669c76956-98rkd -n app-108 -- cat /app/app.sh",
			wantReclassify: false,
		},
		{
			name:           "kubectl rollout history (negative — no grep tail)",
			command:        "kubectl rollout history deployment/app-dev -n nudgebee",
			wantReclassify: false,
		},
		{
			name:           "kubectl describe (negative — no grep tail)",
			command:        "kubectl describe ds kube-proxy -n kube-system",
			wantReclassify: false,
		},
		{
			name:           "kubectl get pods | wc -l (negative — wc is not a no-match command)",
			command:        "kubectl get pods | wc -l",
			wantReclassify: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isNoMatchExit(exitOne, c.command)
			assert.Equal(t, c.wantReclassify, got,
				"isNoMatchExit(%q) — production wants reclassify=%v, got %v", c.command, c.wantReclassify, got)
		})
	}
}
