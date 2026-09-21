package prompts

import (
	"context"
	"strings"
	"testing"
)

func TestK8sLeanPromptIsProtocolNeutralAndDomainScoped(t *testing.T) {
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())

	prompt, err := GetPromptStrict(context.Background(), PromptK8sLean, "")
	if err != nil {
		t.Fatalf("load k8s lean prompt: %v", err)
	}

	for _, protocolToken := range []string{"<thought_action>", "<action>", "<tool_name>", "<tool_input>"} {
		if strings.Contains(prompt, protocolToken) {
			t.Errorf("k8s domain prompt must not prescribe planner protocol %q", protocolToken)
		}
	}
	if strings.Contains(prompt, "full read access") {
		t.Error("k8s prompt must not promise access that the configured tools may not have")
	}

	for _, required := range []string{
		"KUBERNETES EXECUTION EFFICIENCY:",
		"Use the available workspace shell for Kubernetes CLI reads and local computation",
		"Combine related reads, filtering, aggregation, or scripting",
		"multiple requested values can be derived mechanically from the same source stream",
		"Do not add summaries or extra outputs the user did not request",
		"Use `kubectl_execute` instead for every mutation and every command whose effects are uncertain",
		"approval and resume path remains intact",
		"Never hide a mutation inside a shell command",
		"Parallel investigation example (illustrative, not mandatory)",
		"These siblings share a subject but do not depend on one another",
		"finops",
		"automation",
		"service_dependency_graph",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("k8s prompt missing domain policy %q", required)
		}
	}
}
