package prompts

import (
	"context"
	"strings"
	"testing"
)

func TestAwsLeanPromptIsProtocolNeutralAndDomainScoped(t *testing.T) {
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())

	prompt, err := GetPromptStrict(context.Background(), PromptAwsLean, "")
	if err != nil {
		t.Fatalf("load aws lean prompt: %v", err)
	}

	for _, protocolToken := range []string{"<thought_action>", "<action>", "<tool_name>", "<tool_input>"} {
		if strings.Contains(prompt, protocolToken) {
			t.Errorf("aws domain prompt must not prescribe planner protocol %q", protocolToken)
		}
	}
	if strings.Contains(prompt, "full read access") {
		t.Error("aws prompt must not promise access that the configured tools may not have")
	}

	for _, required := range []string{
		"AWS EXECUTION EFFICIENCY:",
		"Use the available workspace shell for AWS CLI reads and local computation",
		"Combine related reads, filtering, aggregation, or scripting",
		"multiple requested values can be derived mechanically from the same source response",
		"Do not add summaries or extra outputs the user did not request",
		"Use `aws_execute` instead for every mutation and every command whose effects are uncertain",
		"approval and resume path remains intact",
		"Never hide a mutation inside a shell command",
		"Parallel investigation example (illustrative, not mandatory)",
		"These siblings share a subject but do not depend on one another",
		"cloud_resource_search_execute",
		"finops",
		"automation",
		"If nothing matches, use `aws_execute`",
		"service_dependency_graph",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("aws prompt missing domain policy %q", required)
		}
	}
}
