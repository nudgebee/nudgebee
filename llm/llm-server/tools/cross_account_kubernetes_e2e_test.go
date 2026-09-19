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

func TestCrossAccountKubernetesVirtualConfigListsAuthorizedContextsE2E(t *testing.T) {
	fixture := loadCrossAccountKubernetesE2E(t)
	response, err := fixture.runShell(t, "kubectl config get-contexts -o name")
	body := requireShellSuccess(t, response, err)
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
