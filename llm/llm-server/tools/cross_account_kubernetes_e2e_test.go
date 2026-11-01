//go:build e2e

package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type crossAccountKubernetesE2E struct {
	tenant             string
	user               string
	workspaceAccount   string
	defaultContext     string
	defaultExpectedUID string
	context1           string
	account1           string
	expectedUID1       string
	context2           string
	account2           string
	expectedUID2       string
	unauthorized       string
}

// Run against a deployed llm-server and live workspace/relay stack. All
// commands are read-only; namespace UIDs give each cluster a stable identity.
//
// Required environment:
//
//	CROSS_ACCOUNT_K8S_E2E=1
//	TEST_TENANT, TEST_USER
//	CROSS_ACCOUNT_WORKSPACE_ACCOUNT
//	CROSS_ACCOUNT_DEFAULT_CONTEXT, CROSS_ACCOUNT_DEFAULT_EXPECTED_UID
//	CROSS_ACCOUNT_CONTEXT_1, CROSS_ACCOUNT_ACCOUNT_1, CROSS_ACCOUNT_EXPECTED_UID_1
//	CROSS_ACCOUNT_CONTEXT_2, CROSS_ACCOUNT_ACCOUNT_2, CROSS_ACCOUNT_EXPECTED_UID_2
//	CROSS_ACCOUNT_RESOURCE_NAME_2 (for TestCrossAccountResourceSearchE2E; unique to context 2)
//
// Optional: CROSS_ACCOUNT_UNAUTHORIZED_CONTEXT names a connected context the
// caller cannot access. When provided, the suite verifies that it fails closed.
func loadCrossAccountKubernetesE2E(t *testing.T) crossAccountKubernetesE2E {
	t.Helper()
	if os.Getenv("CROSS_ACCOUNT_K8S_E2E") != "1" {
		t.Skip("set CROSS_ACCOUNT_K8S_E2E=1 and the CROSS_ACCOUNT_* variables documented in this test")
	}
	required := func(key string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(key))
		require.NotEmpty(t, value, "%s must be set", key)
		return value
	}
	fixture := crossAccountKubernetesE2E{
		tenant:             required("TEST_TENANT"),
		user:               required("TEST_USER"),
		workspaceAccount:   required("CROSS_ACCOUNT_WORKSPACE_ACCOUNT"),
		defaultContext:     required("CROSS_ACCOUNT_DEFAULT_CONTEXT"),
		defaultExpectedUID: required("CROSS_ACCOUNT_DEFAULT_EXPECTED_UID"),
		context1:           required("CROSS_ACCOUNT_CONTEXT_1"),
		account1:           required("CROSS_ACCOUNT_ACCOUNT_1"),
		expectedUID1:       required("CROSS_ACCOUNT_EXPECTED_UID_1"),
		context2:           required("CROSS_ACCOUNT_CONTEXT_2"),
		account2:           required("CROSS_ACCOUNT_ACCOUNT_2"),
		expectedUID2:       required("CROSS_ACCOUNT_EXPECTED_UID_2"),
		unauthorized:       strings.TrimSpace(os.Getenv("CROSS_ACCOUNT_UNAUTHORIZED_CONTEXT")),
	}
	require.NotEqual(t, fixture.context1, fixture.context2, "the test requires two distinct contexts")
	require.NotEqual(t, fixture.account1, fixture.account2, "the test requires two distinct target accounts")
	return fixture
}

func (f crossAccountKubernetesE2E) runShell(t *testing.T, command string) (core.NBToolResponse, error) {
	t.Helper()
	shell := ShellTool{AccountId: f.workspaceAccount}
	sc := security.NewRequestContextForTenantAccountAdmin(f.tenant, f.user, []string{
		f.workspaceAccount,
		f.account1,
		f.account2,
	})
	ctx := core.NewNbToolContext(
		sc,
		shell,
		f.workspaceAccount,
		f.user,
		"cross-account-k8s-e2e-"+uuid.NewString(),
		uuid.NewString(),
		uuid.NewString(),
		"",
		nil,
		"",
		core.NBQueryConfig{ToolConfigs: map[string]string{ToolExecuteKubectlCommand: f.defaultContext}},
		uuid.NewString(),
	)
	return shell.Call(ctx, core.NBToolCallRequest{Command: command})
}

func requireShellSuccess(t *testing.T, response core.NBToolResponse, err error) string {
	t.Helper()
	require.NoError(t, err, "shell response: %s", response.Data)
	require.Equal(t, core.NBToolResponseStatusSuccess, response.Status, "shell response: %s", response.Data)
	return response.Data
}

// TestCrossAccountHelmE2E exercises the direct Helm tool through the workspace
// shim and local workspace proxy. It intentionally uses read-only commands.
// In addition to TEST_TENANT and TEST_USER, set HELM_E2E_WORKSPACE_ACCOUNT,
// HELM_E2E_TARGET_ACCOUNT, and HELM_E2E_TARGET_NAME.
func TestCrossAccountHelmE2E(t *testing.T) {
	if os.Getenv("HELM_CROSS_ACCOUNT_E2E") != "1" {
		t.Skip("set HELM_CROSS_ACCOUNT_E2E=1 and the HELM_E2E_* variables")
	}
	required := func(key string) string {
		t.Helper()
		value := strings.TrimSpace(os.Getenv(key))
		require.NotEmpty(t, value, "%s must be set", key)
		return value
	}
	workspaceAccount := required("HELM_E2E_WORKSPACE_ACCOUNT")
	targetAccount := required("HELM_E2E_TARGET_ACCOUNT")
	targetName := required("HELM_E2E_TARGET_NAME")
	command := strings.TrimSpace(os.Getenv("HELM_E2E_COMMAND"))
	if command == "" {
		command = "helm list -A"
	}
	ctx := security.NewRequestContextForTenantAccountAdmin(required("TEST_TENANT"), required("TEST_USER"), []string{workspaceAccount, targetAccount})
	helm := HelmExecuteTool{}
	toolCtx := core.NewNbToolContext(ctx, helm, workspaceAccount, required("TEST_USER"),
		"cross-account-helm-e2e-"+uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", core.NBQueryConfig{}, uuid.NewString())
	response, err := helm.Call(toolCtx, core.NBToolCallRequest{Command: command, Arguments: map[string]any{"cluster": targetName}})
	require.NoError(t, err, "helm response: %s", response.Data)
	require.Equal(t, core.NBToolResponseStatusSuccess, response.Status, "helm response: %s", response.Data)
	var result map[string]string
	require.NoError(t, json.Unmarshal([]byte(response.Data), &result))
	require.Equal(t, targetName, result["cluster"])
	require.Contains(t, result, "stdout")

	// The default call must stay on the workspace's account, while the shell
	// shim must route a concrete --kube-context independently of tool metadata.
	defaultResponse, err := helm.Call(toolCtx, core.NBToolCallRequest{Command: "helm version --short"})
	require.NoError(t, err, "default helm response: %s", defaultResponse.Data)
	require.Equal(t, core.NBToolResponseStatusSuccess, defaultResponse.Status)
	var defaultResult map[string]string
	require.NoError(t, json.Unmarshal([]byte(defaultResponse.Data), &defaultResult))
	require.NotContains(t, defaultResult, "cluster")

	shell := ShellTool{AccountId: workspaceAccount}
	shellCtx := core.NewNbToolContext(ctx, shell, workspaceAccount, required("TEST_USER"),
		"cross-account-helm-shell-e2e-"+uuid.NewString(), uuid.NewString(), uuid.NewString(), "", nil, "", core.NBQueryConfig{}, uuid.NewString())
	shellResponse, err := shell.Call(shellCtx, core.NBToolCallRequest{Command: "helm --kube-context " + common.ShellEscape(targetName) + " version --short"})
	require.NoError(t, err, "shell helm response: %s", shellResponse.Data)
	require.Equal(t, core.NBToolResponseStatusSuccess, shellResponse.Status)

	_, err = helm.Call(toolCtx, core.NBToolCallRequest{Command: "helm version --short", Arguments: map[string]any{"cluster": "nonexistent-cross-account-helm-test"}})
	require.Error(t, err, "unknown clusters must fail closed")
	_, err = helm.Call(toolCtx, core.NBToolCallRequest{Command: "helm --kube-context k8s-dev version --short", Arguments: map[string]any{"cluster": targetName}})
	require.ErrorContains(t, err, "disagree")
	blockedResponse, err := helm.Call(toolCtx, core.NBToolCallRequest{Command: "helm version --short --kube-apiserver=https://example.invalid", Arguments: map[string]any{"cluster": targetName}})
	require.Error(t, err)
	require.Contains(t, blockedResponse.Data, "not allowed")
}

func TestCrossAccountKubernetesPythonUsesTwoContextsE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	contexts, err := json.Marshal([]string{fixture.context1, fixture.context2})
	require.NoError(t, err)
	script := fmt.Sprintf(`import json, subprocess
contexts = json.loads(%q)
for context in contexts:
    uid = subprocess.check_output([
        "kubectl", "--context", context, "get", "namespace", "kube-system",
        "-o", "jsonpath={.metadata.uid}",
    ], text=True)
    print(context + "=" + uid)
`, string(contexts))

	response, callErr := fixture.runShell(t, "python3 -c "+common.ShellEscape(script))
	body := requireShellSuccess(t, response, callErr)
	require.Contains(t, body, fixture.context1+"="+fixture.expectedUID1)
	require.Contains(t, body, fixture.context2+"="+fixture.expectedUID2)
}

// A shell loop picks each cluster through a variable. The script text names
// none; each expanded kubectl invocation must run on the cluster it names.
func TestCrossAccountKubernetesShellLoopUsesEachContextE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	response, callErr := fixture.runShell(t, fmt.Sprintf(
		`for c in %s %s; do echo "$c=$(kubectl --context $c get namespace kube-system -o jsonpath='{.metadata.uid}')"; done`,
		fixture.context1, fixture.context2))
	body := requireShellSuccess(t, response, callErr)
	require.Contains(t, body, fixture.context1+"="+fixture.expectedUID1)
	require.Contains(t, body, fixture.context2+"="+fixture.expectedUID2)
}

func TestCrossAccountKubernetesVirtualConfigListsAuthorizedContextsE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	response, err := fixture.runShell(t, "kubectl config get-contexts -o name")
	body := requireShellSuccess(t, response, err)
	require.Contains(t, body, fixture.context1)
	require.Contains(t, body, fixture.context2)
	if fixture.unauthorized != "" {
		require.NotContains(t, body, fixture.unauthorized)
	}

	response, err = fixture.runShell(t, "kubectl config get-clusters")
	body = requireShellSuccess(t, response, err)
	require.Contains(t, body, fixture.context1)
	require.Contains(t, body, fixture.context2)
	if fixture.unauthorized != "" {
		require.NotContains(t, body, fixture.unauthorized)
	}
}

func TestCrossAccountKubernetesUnauthorizedContextFailsClosedE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	if fixture.unauthorized == "" {
		t.Skip("set CROSS_ACCOUNT_UNAUTHORIZED_CONTEXT to run the fail-closed check")
	}
	command := "kubectl --context " + common.ShellEscape(fixture.unauthorized) + " get namespace kube-system"
	response, err := fixture.runShell(t, command)
	require.Error(t, err, "unauthorized context unexpectedly succeeded: %s", response.Data)
	require.Equal(t, core.NBToolResponseStatusError, response.Status)
	require.Contains(t, strings.ToLower(response.Data), "not connected or you do not have access")
}

func TestCrossAccountKubernetesNoContextUsesDefaultE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	response, err := fixture.runShell(t, "kubectl get namespace kube-system -o jsonpath='{.metadata.uid}'")
	body := requireShellSuccess(t, response, err)
	require.Contains(t, body, fixture.defaultExpectedUID)
}

func TestCrossAccountKubernetesExitStatusAndStderrE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	command := "kubectl --context " + common.ShellEscape(fixture.context1) + " get definitely-not-a-kubernetes-resource"
	response, err := fixture.runShell(t, command)
	require.Error(t, err, "invalid kubectl resource unexpectedly succeeded: %s", response.Data)
	require.Equal(t, core.NBToolResponseStatusError, response.Status)
	require.NotContains(t, response.Data, `workspace command failed: status="failed" error="exit status 1"`)
	require.True(t,
		strings.Contains(response.Data, "doesn't have a resource type") ||
			strings.Contains(response.Data, "no matches for kind") ||
			strings.Contains(strings.ToLower(response.Data), "error"),
		"expected actionable kubectl stderr, got: %s", response.Data,
	)
}

// TestCrossAccountResourceSearchE2E exercises the resource_search_execute seam
// added on top of the shared per-call Kubernetes target contract. The named
// resource should exist in context2's inventory; use a resource unique to that
// cluster so a wrong-account lookup cannot accidentally pass.
func TestCrossAccountResourceSearchE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	resourceName := strings.TrimSpace(os.Getenv("CROSS_ACCOUNT_RESOURCE_NAME_2"))
	require.NotEmpty(t, resourceName, "CROSS_ACCOUNT_RESOURCE_NAME_2 must name a resource unique to CROSS_ACCOUNT_CONTEXT_2")

	tool := K8sResourceSearchTool{}
	sc := security.NewRequestContextForTenantAccountAdmin(fixture.tenant, fixture.user, []string{
		fixture.workspaceAccount,
		fixture.account1,
		fixture.account2,
	})
	origin := core.ToolConfig{Name: fixture.context1, Values: []core.ToolConfigValue{{Name: "id", Value: fixture.account1}}}
	newContext := func() core.NbToolContext {
		return core.NbToolContext{
			Ctx:            sc,
			AccountId:      fixture.workspaceAccount,
			UserId:         fixture.user,
			ConversationId: "resource-search-e2e-" + uuid.NewString(),
			MessageId:      uuid.NewString(),
			ToolCallId:     uuid.NewString(),
			ToolConfig:     origin,
		}
	}

	t.Run("omitted cluster keeps the saved target", func(t *testing.T) {
		response, err := tool.Call(newContext(), core.NBToolCallRequest{
			Command: `{"resource_type":"podss","search_type":"fuzzy"}`,
			Arguments: map[string]any{
				"resource_type": "podss",
				"search_type":   "fuzzy",
			},
		})
		require.NoError(t, err, "response: %s", response.Data)
		require.Equal(t, fixture.account1, response.AdditionalDetails["target_account_id"])
		require.Equal(t, fixture.context1, response.AdditionalDetails["target_name"])
	})

	t.Run("explicit cluster scopes inventory and provenance", func(t *testing.T) {
		command, err := common.MarshalJson(map[string]any{
			"resource_name": resourceName,
			"resource_type": "pod",
			"search_type":   "suggestions",
			"cluster":       fixture.context2,
		})
		require.NoError(t, err)
		response, callErr := tool.Call(newContext(), core.NBToolCallRequest{
			Command: string(command),
			Arguments: map[string]any{
				"resource_name": resourceName,
				"resource_type": "pod",
				"search_type":   "suggestions",
				"cluster":       fixture.context2,
			},
		})
		require.NoError(t, callErr, "response: %s", response.Data)
		require.Equal(t, fixture.account2, response.AdditionalDetails["target_account_id"])
		require.Equal(t, fixture.context2, response.AdditionalDetails["target_name"])
		require.Equal(t, fixture.workspaceAccount, response.AdditionalDetails["origin_account_id"])
		require.NotNil(t, response.Metadata)
		require.Greater(t, response.Metadata.DBQueries, 0, "the call must exercise the target-scoped inventory lookup")
		require.Greater(t, response.Metadata.RelayCalls, 0, "owner enrichment must exercise the target-bound live kubectl path")

		var result K8sResourceSearchResponse
		require.NoError(t, common.UnmarshalJson([]byte(response.Data), &result))
		found := false
		for _, resource := range result.Resources {
			if resource.Name == resourceName {
				found = true
				break
			}
		}
		require.True(t, found, "target resource %q not found in response: %s", resourceName, response.Data)

		var payload map[string]any
		require.NoError(t, common.UnmarshalJson([]byte(response.Data), &payload))
		target, ok := payload["target"].(map[string]any)
		require.True(t, ok, "target provenance missing: %s", response.Data)
		require.Equal(t, fixture.context2, target["name"])
		require.Equal(t, fixture.account2, target["account_id"])
	})
}
