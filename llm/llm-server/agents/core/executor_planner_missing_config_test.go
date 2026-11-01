package core

import (
	"testing"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// #39826: from a conversation with no default config for a cloud (a k8s
// cluster asking about AWS), a call naming a connected account was refused
// before reaching Call, which resolves and access-checks the named account.
func TestMissingToolConfig_NamedTargetReachesCall(t *testing.T) {
	none := toolcore.ToolConfig{}
	configured := toolcore.ToolConfig{Name: "dev-aws"}

	for name, tc := range map[string]struct {
		tool    toolcore.NBTool
		input   string
		config  toolcore.ToolConfig
		missing bool
	}{
		"aws, account named, no default":     {tools.AwsCliTool{}, `{"account":"dev-aws","command":"aws sts get-caller-identity"}`, none, false},
		"gcp, account named, no default":     {tools.GcpCliTool{}, `{"account":"gcp-dev","command":"gcloud projects list"}`, none, false},
		"azure, account named, no default":   {tools.AzureCliTool{}, `{"account":"azure-dev","command":"az group list"}`, none, false},
		"kubectl, cluster named, no default": {tools.KubectlExecuteTool{}, `{"cluster":"k8s-prod","command":"kubectl get ns"}`, none, false},
		"kubectl, --context, no default":     {tools.KubectlExecuteTool{}, `{"command":"kubectl --context k8s-prod get ns"}`, none, false},
		"helm, --kube-context, no default":   {tools.HelmExecuteTool{}, `{"command":"helm list --kube-context k8s-prod"}`, none, false},
		"resource search, cluster named":     {tools.K8sResourceSearchTool{}, `{"cluster":"k8s-prod","query":"app-prod"}`, none, false},
		"aws, nothing named, no default":     {tools.AwsCliTool{}, `{"command":"aws sts get-caller-identity"}`, none, true},
		"aws, nothing named, default":        {tools.AwsCliTool{}, `{"command":"aws sts get-caller-identity"}`, configured, false},
		"aws, plain-text input, no default":  {tools.AwsCliTool{}, `aws sts get-caller-identity`, none, true},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.missing, missingToolConfig(tc.tool, tc.input, tc.config))
		})
	}
}
