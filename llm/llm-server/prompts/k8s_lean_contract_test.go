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
		"Keep shell output limited to the evidence and mechanical aggregation needed for the current question",
		"This does not restrict the evidence synthesis required in your final answer",
		"Use `kubectl_execute` instead for every mutation and every command whose effects are uncertain",
		"approval and resume path remains intact",
		"Never hide a mutation inside a shell command",
		"Parallel investigation example (illustrative, not mandatory)",
		"one bounded Kubernetes state snapshot (`get`/`describe`/`top`, compounded when useful)",
		"`events`, `logs`, `metrics`, `traces`, and `service_dependency_graph`",
		"Use only the evidence domains relevant to the question",
		"These siblings share a subject but do not depend on one another",
		"If nothing matches, use `kubectl_execute`",
		"During an investigation, NEVER restart/scale/roll back/drain/rotate/clear",
		"Every specific claim (resource state, configuration value, event, or relationship) MUST trace to a successful tool observation",
		"finops",
		"automation",
		"service_dependency_graph",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("k8s prompt missing domain policy %q", required)
		}
	}
}
