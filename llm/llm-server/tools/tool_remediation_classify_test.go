package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// Remediation commands are almost all kubectl: the kubectl rules decide them
// without an LLM call. Anything else is left to the remediation prompt.
func TestRemediationCommandsUseTheShellRules(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	cases := map[string]core.ToolRequestType{
		`{"command":"kubectl scale deployment --all --replicas=0 -n staging"}`:            core.ToolRequestTypeUpdate,
		`{"command":"kubectl patch deployment cart -n demo -p '{\"spec\":{}}'"}`:          core.ToolRequestTypeUpdate,
		`{"command":"kubectl delete pod cart-1 -n demo"}`:                                 core.ToolRequestTypeDelete,
		`{"command":"kubectl get pods -n demo -l app=cart","namespace":"demo"}`:           core.ToolRequestTypeRead,
		`{"command":"kubectl rollout status deployment/cart -n demo","namespace":"demo"}`: core.ToolRequestTypeRead,
		`{"command":"kubectl get pods -n demo && kubectl delete pod cart-1 -n demo"}`:     core.ToolRequestTypeDelete,
		// Not decided here: the remediation prompt classifies these (the
		// kubectl rules leave rollout restart to a prompt too).
		`{"command":"kubectl rollout restart deployment quote -n demo","namespace":"demo"}`: "",
		`{"command":"systemctl restart nginx"}`:                                             "",
		`{"command":"kubectl get pods -n demo > /tmp/pods.txt"}`:                            "",
	}
	for input, want := range cases {
		got, err := RemediationExecuteTool{}.InferToolRequestType(ctx, ToolRemediationExecute, input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

// A second, differently cased "command" key must not decide the verdict: the
// executor runs the exact "command" key.
func TestClassifiersReadTheCommandKeyTheExecutorRuns(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	input := `{"command":"kubectl delete pod x -n demo","Command":"kubectl get pods -n demo"}`
	assert.Equal(t, "kubectl delete pod x -n demo", extractCommandFromToolInput(input))

	got, err := RemediationExecuteTool{}.InferToolRequestType(ctx, ToolRemediationExecute, input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeDelete, got)
	got, err = ShellTool{}.InferToolRequestType(ctx, "shell_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeDelete, got)
	got, err = KubectlExecuteTool{}.InferToolRequestType(ctx, "kubectl_execute", input)
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeDelete, got)

	// Only a differently cased key: not what the executor runs, so no static verdict on it.
	assert.Equal(t, `{"Command":"kubectl get pods"}`, extractCommandFromToolInput(`{"Command":"kubectl get pods"}`))
}
