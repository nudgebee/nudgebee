package tools

import (
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedKubectlWorkspace answers ExecuteOrLazyCreate from respond and records
// every command it was asked to run.
type scriptedKubectlWorkspace struct {
	workspace.WorkspaceManager
	respond func(command string) string
	delay   time.Duration

	mu       sync.Mutex
	commands []string
}

func (s *scriptedKubectlWorkspace) ExecuteOrLazyCreate(ctx *security.RequestContext, accountId, conversationId, command string, env map[string]string) (string, error) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
	time.Sleep(s.delay)
	return s.respond(command), nil
}

func (s *scriptedKubectlWorkspace) ran() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func useScriptedWorkspace(t *testing.T, s workspace.WorkspaceManager) core.NbToolContext {
	t.Helper()
	origWm := wm
	wm = s
	t.Cleanup(func() { wm = origWm })
	return core.NbToolContext{
		Ctx:            security.NewRequestContextForSuperAdmin(),
		AccountId:      "acct-1",
		ConversationId: "conv-1",
		Stats:          &core.ToolCallStats{},
	}
}

// TestExecuteKubectlCommand_IdenticalCommandRunsOncePerCall guards the
// per-call cache: concurrent and repeated requests for one command reach the
// workspace once, and every caller gets that run's output.
func TestExecuteKubectlCommand_IdenticalCommandRunsOncePerCall(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return "web-1 1/1 Running 0 1d" }, delay: 20 * time.Millisecond}
	ctx := useScriptedWorkspace(t, ws)
	tool := K8sResourceSearchTool{cmdCache: newKubectlCommandCache()}

	var wg sync.WaitGroup
	outputs := make([]string, 8)
	for i := range outputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			outputs[i] = tool.executeKubectlCommand("kubectl get pods -n shop --no-headers", ctx)
		}(i)
	}
	wg.Wait()
	tool.executeKubectlCommand("kubectl get pods -n shop --no-headers", ctx)
	tool.executeKubectlCommand("kubectl get pods -n other --no-headers", ctx)

	assert.Equal(t, []string{"kubectl get pods -n shop --no-headers", "kubectl get pods -n other --no-headers"}, ws.ran())
	for _, out := range outputs {
		assert.Equal(t, "web-1 1/1 Running 0 1d", out)
	}
}

// TestExecuteKubectlCommand_NoCacheOutsideCall keeps direct callers (tests,
// helpers used without Call) running every command.
func TestExecuteKubectlCommand_NoCacheOutsideCall(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return "x" }}
	ctx := useScriptedWorkspace(t, ws)
	tool := K8sResourceSearchTool{}

	tool.executeKubectlCommand("kubectl get nodes --no-headers", ctx)
	tool.executeKubectlCommand("kubectl get nodes --no-headers", ctx)

	assert.Len(t, ws.ran(), 2)
}

// TestSearchResourceTypes_OneKubectlCallForAllTypes checks the collapsed
// search: one comma-separated kubectl call, rows reported under plural type
// names, and matching on namespace/name only — "config" must not match every
// ConfigMap through its kind column. The fixture is real kubectl output shape
// (custom-columns, verified against a live cluster).
func TestSearchResourceTypes_OneKubectlCallForAllTypes(t *testing.T) {
	output := strings.Join([]string{
		"Pod          shop   app-config-loader-7d9f   Running",
		"ConfigMap    shop   kube-root-ca.crt         <none>",
		"Deployment   shop   app-config-loader        <none>",
		"ReplicaSet   shop   app-config-loader-7d9f   <none>",
		"CronJob      shop   app-config-cleanup       <none>",
	}, "\n")
	ws := &scriptedKubectlWorkspace{respond: func(cmd string) string {
		if strings.HasSuffix(cmd, "| grep -i -- 'config'") {
			return output
		}
		return ""
	}}
	ctx := useScriptedWorkspace(t, ws)

	got := K8sResourceSearchTool{}.searchResourceTypes("config", "--all-namespaces", namespacedSearchTypes, ctx)

	require.Equal(t, []string{
		"kubectl get all,configmaps --all-namespaces " + kindColumns + " | grep -i -- 'config'",
	}, ws.ran())
	var names []string
	for _, r := range got {
		names = append(names, r.Type+":"+r.Namespace+"/"+r.Name+":"+r.Status)
		assert.Equal(t, resourceSourceCluster, r.Source)
	}
	assert.Equal(t, []string{
		"pods:shop/app-config-loader-7d9f:Running",
		"deployments:shop/app-config-loader:",
		"cronjobs:shop/app-config-cleanup:",
	}, names)
}

func TestSearchResourceTypes_ClusterScopedAndNamespaceFlag(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(cmd string) string {
		if strings.Contains(cmd, "clusterroles") {
			return "ClusterRole    <none>   payments-reader   <none>\nStorageClass   <none>   payments-ssd      <none>"
		}
		return ""
	}}
	ctx := useScriptedWorkspace(t, ws)
	tool := K8sResourceSearchTool{}

	got := tool.searchResourceTypes("payments", "", clusterSearchTypes, ctx)
	tool.searchResourceTypes("payments", "shop", []string{"pods", "services"}, ctx)

	assert.Equal(t, []string{
		"kubectl get clusterroles,clusterrolebindings,nodes,persistentvolumes,storageclasses,customresourcedefinitions " + kindColumns + " | grep -i -- 'payments'",
		"kubectl get pods,services -n shop " + kindColumns + " | grep -i -- 'payments'",
	}, ws.ran())
	require.Len(t, got, 2)
	assert.Equal(t, "clusterroles", got[0].Type)
	assert.Equal(t, "", got[0].Namespace)
	assert.Equal(t, "storageclasses", got[1].Type)
}

// TestCommonResourceTypes_PassKubectlAccessCheck: one blocked kind (secrets)
// in the combined list rejects the whole command, so every listed kind must
// pass the same access check executeKubectlCommand applies.
func TestCommonResourceTypes_PassKubectlAccessCheck(t *testing.T) {
	for _, types := range [][]string{namespacedSearchTypes, clusterSearchTypes} {
		cmd := "kubectl get " + strings.Join(types, ",") + " -A " + kindColumns + " | grep -i -- 'x'"
		assert.NoError(t, validateKubectlCommandAccess(cmd), cmd)
	}
}

func TestResourceTypeForKind(t *testing.T) {
	for kind, want := range map[string]string{
		"Pod":                "pods",
		"StorageClass":       "storageclasses",
		"ClusterRoleBinding": "clusterrolebindings",
		"Rollout":            "rollout",
		"ReplicaSet":         "replicaset",
	} {
		assert.Equal(t, want, resourceTypeForKind(kind), kind)
	}
}

func TestLineMatchesSearch(t *testing.T) {
	terms, variations := resourceSearchTerms("LLM server")
	assert.True(t, lineMatchesSearch("nudgebee llm-server-6f9", terms, variations))
	assert.True(t, lineMatchesSearch("server-for-llm", terms, variations), "all terms present")
	assert.False(t, lineMatchesSearch("llm-gateway", terms, variations))
	assert.True(t, lineMatchesSearch("anything", nil, nil), "no terms matches everything")
}

// TestSearchResourceTypes_NoTermsListsPerKind: without a grep pipe one
// forbidden kind would fail a combined call, so an empty name lists each kind
// separately.
func TestSearchResourceTypes_NoTermsListsPerKind(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return "" }}
	ctx := useScriptedWorkspace(t, ws)

	K8sResourceSearchTool{}.searchResourceTypes("", "", []string{"nodes", "storageclasses"}, ctx)

	assert.Equal(t, []string{"kubectl get nodes --no-headers", "kubectl get storageclasses --no-headers"}, ws.ran())
}

// TestSearchResourceTypes_NoTermsExpandsAllCategory: "all" is a category the
// per-kind parser can't read, so with no name it is listed kind by kind.
func TestSearchResourceTypes_NoTermsExpandsAllCategory(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return "" }}
	ctx := useScriptedWorkspace(t, ws)

	K8sResourceSearchTool{}.searchResourceTypes("", "--all-namespaces", namespacedSearchTypes, ctx)

	var want []string
	for _, kind := range append(append([]string{}, allCategoryKinds...), "configmaps") {
		want = append(want, "kubectl get "+kind+" --all-namespaces --no-headers")
	}
	assert.Equal(t, want, ws.ran())
}

// TestGrepKubectlOutput_TermSelection: specific terms are grepped (last
// first) and generic ones skipped; when every term is generic they are all
// tried instead of none.
func TestGrepKubectlOutput_TermSelection(t *testing.T) {
	cases := map[string][]string{
		"llm server": {"kubectl get x | grep -i -- 'llm'"},
		"my pod":     {"kubectl get x | grep -i -- 'pod'", "kubectl get x | grep -i -- 'my'"},
		"pod":        {"kubectl get x | grep -i -- 'pod'"},
	}
	for name, want := range cases {
		ws := &scriptedKubectlWorkspace{respond: func(string) string { return "" }}
		ctx := useScriptedWorkspace(t, ws)
		terms, _ := resourceSearchTerms(name)
		K8sResourceSearchTool{}.grepKubectlOutput("kubectl get x", terms, ctx)
		assert.Equal(t, want, ws.ran(), name)
	}
}

// TestFindActualResources_CRDTypesOneCallWithoutBlockedKinds: custom types are
// searched in one combined call, and a Secret-bearing CRD the access check
// blocks is left out rather than rejecting that call, and rollouts (already
// searched on its own) is not searched twice.
func TestFindActualResources_CRDTypesOneCallWithoutBlockedKinds(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(cmd string) string {
		if cmd == "kubectl get customresourcedefinitions --no-headers" {
			return "servicemonitors.monitoring.coreos.com   2024-01-01T00:00:00Z\nsealedsecrets.bitnami.com   2024-01-01T00:00:00Z\nrollouts.argoproj.io   2024-01-01T00:00:00Z\nprometheusrules.monitoring.coreos.com   2024-01-01T00:00:00Z"
		}
		return ""
	}}
	ctx := useScriptedWorkspace(t, ws)

	K8sResourceSearchTool{}.findActualResources("payments", "--all-namespaces", "", ctx)

	var crdCalls []string
	for _, cmd := range ws.ran() {
		if strings.Contains(cmd, "servicemonitors") || strings.Contains(cmd, "sealedsecrets") {
			crdCalls = append(crdCalls, cmd)
		}
	}
	assert.Equal(t, []string{
		"kubectl get servicemonitors,prometheusrules --all-namespaces " + kindColumns + " | grep -i -- 'payments'",
	}, crdCalls)
}

func TestGrepKubectlOutput_EscapesQuotes(t *testing.T) {
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return "" }}
	ctx := useScriptedWorkspace(t, ws)

	K8sResourceSearchTool{}.grepKubectlOutput("kubectl get x", []string{"worker's"}, ctx)

	assert.Equal(t, []string{`kubectl get x | grep -i -- 'worker'\''s'`}, ws.ran())
}

func TestPodLabelsMatch(t *testing.T) {
	assert.True(t, podLabelsMatch("k8s-app=kube-dns,pod-template-hash=55cb58b774", "kube-dns"))
	assert.True(t, podLabelsMatch("app.kubernetes.io/instance=payments", "payments"))
	assert.False(t, podLabelsMatch("k8s-app=kube-dns", "kube"), "exact value, not substring")
	assert.False(t, podLabelsMatch("app=Payments", "payments"), "case-sensitive like a selector")
	assert.False(t, podLabelsMatch("tier=payments", "payments"), "only the searched label keys")
	assert.False(t, podLabelsMatch("<none>", "payments"))
}

// TestSearchPodsByLabelValue: one call, rows parsed with the existing pod
// parser (same Type/Ready/Status as a -l selector lookup), only exact matches.
func TestSearchPodsByLabelValue(t *testing.T) {
	output := strings.Join([]string{
		"kube-system   coredns-55cb58b774-9zm5l   1/1   Running   28 (2d4h ago)   247d   k8s-app=kube-dns,pod-template-hash=55cb58b774",
		"kube-system   kube-dns-autoscaler-abc    1/1   Running   0               9d     k8s-app=kube-dns-autoscaler",
	}, "\n")
	ws := &scriptedKubectlWorkspace{respond: func(string) string { return output }}
	ctx := useScriptedWorkspace(t, ws)

	got := K8sResourceSearchTool{}.searchPodsByLabelValue("kube-dns", "--all-namespaces", ctx)

	assert.Equal(t, []string{"kubectl get pods --all-namespaces --show-labels --no-headers | grep -i -- 'kube-dns'"}, ws.ran())
	require.Len(t, got, 1)
	assert.Equal(t, K8sResourceInfo{Source: resourceSourceCluster, Name: "coredns-55cb58b774-9zm5l", Namespace: "kube-system", Type: "pod", Ready: "1/1", Status: "Running", Age: "(2d4h"}, got[0])
}

// TestLabelLookup_SameCommandInBothPasses: for a single-word lowercase name
// the strategy list's label lookup is byte-identical to the fan's, so the
// per-call cache answers it without another kubectl call.
func TestLabelLookup_SameCommandInBothPasses(t *testing.T) {
	strategies := K8sResourceSearchTool{}.generateResourceSearchStrategies("payment-api", "--all-namespaces")
	require.Equal(t, "label_match", strategies[0].Strategy)
	assert.Equal(t, "payment-api", strategies[0].LabelValue)
	assert.Equal(t, podsByLabelValueCommand("payment-api", "--all-namespaces"), strategies[0].Command)

	multi := K8sResourceSearchTool{}.generateResourceSearchStrategies("Payment Service", "shop")
	assert.Equal(t, "payment-service", multi[0].LabelValue)
	assert.Equal(t, "kubectl get pods -n shop --show-labels --no-headers | grep -i -- 'payment-service'", multi[0].Command)
}
