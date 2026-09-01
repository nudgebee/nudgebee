package prompts

import (
	"context"
	"strings"
	"testing"
)

func TestAzureAndGcpLeanPromptsAreProtocolNeutralAndDomainScoped(t *testing.T) {
	oldLoader := GetLoader()
	t.Cleanup(func() { SetGlobalLoaderForTesting(oldLoader) })
	SetGlobalLoaderForTesting(NewLoaderForTesting())

	tests := []struct {
		name     string
		promptID string
		required []string
	}{
		{
			name:     "azure",
			promptID: PromptAzureLean,
			required: []string{
				"AZURE EXECUTION EFFICIENCY:",
				"Use `logs`, `events`, `metrics`, `traces`, and `service_dependency_graph` for observability evidence",
				"Use workspace Azure CLI reads for resource state, configuration, and declared intent",
				"Do not reproduce an available observability tool's work through the Azure CLI",
				"Use the available workspace shell for Azure CLI reads and local computation",
				"Use `azure_execute` instead for every mutation",
				"every `az vm run-command invoke` call",
				"approval and resume behavior remains intact",
				"Never hide a mutation or remote command execution inside a shell command",
				"If nothing matches, use `azure_execute`",
			},
		},
		{
			name:     "gcp",
			promptID: PromptGcpLean,
			required: []string{
				"GCP EXECUTION EFFICIENCY:",
				"Use `logs`, `events`, `metrics`, `traces`, and `service_dependency_graph` for observability evidence",
				"Use workspace GCP CLI reads for resource state, configuration, and declared intent",
				"Do not reproduce an available observability tool's work through the GCP CLI",
				"Use the available workspace shell for GCP CLI reads, read-only REST calls, and local computation",
				"Use `gcloud_execute` instead for every supported mutation",
				"Keep `bq` and `gsutil` shell use to explicit reads",
				"A `bq query`, including SELECT, creates a server-side job",
				"approval and resume path remains intact",
				"Never hide a mutation inside a shell command",
				"If nothing matches, use `gcloud_execute`",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt, err := GetPromptStrict(context.Background(), tc.promptID, "")
			if err != nil {
				t.Fatalf("load %s lean prompt: %v", tc.name, err)
			}
			for _, protocolToken := range []string{"<thought_action>", "<action>", "<tool_name>", "<tool_input>"} {
				if strings.Contains(prompt, protocolToken) {
					t.Errorf("%s domain prompt must not prescribe planner protocol %q", tc.name, protocolToken)
				}
			}
			if strings.Contains(prompt, "full read access") {
				t.Errorf("%s prompt must not promise access that configured tools may not have", tc.name)
			}
			for _, required := range append(tc.required,
				"Combine related reads, filtering, aggregation, or scripting",
				"multiple requested values can be derived mechanically from the same source response",
				"Keep shell output limited to the evidence and mechanical aggregation needed for the current question",
				"This does not restrict the evidence synthesis required in your final answer",
				"Parallel investigation example (illustrative, not mandatory)",
				"`events`, `logs`, `metrics`, `traces`, and `service_dependency_graph`",
				"Use only the evidence domains relevant to the question",
				"These siblings share a subject but do not depend on one another",
				"finops", "automation", "service_dependency_graph") {
				if !strings.Contains(prompt, required) {
					t.Errorf("%s prompt missing domain policy %q", tc.name, required)
				}
			}
		})
	}
}
