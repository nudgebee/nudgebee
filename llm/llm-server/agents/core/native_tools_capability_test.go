package core

import "testing"

func TestSupportsNativeTools(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{"bedrock", "bedrock", "anthropic.claude-3", true},
		{"azure", "azure", "gpt-4o", true},
		{"googleai", "googleai", "gemini-2.0-flash", true},
		{"vertexai", "vertexai", "gemini-1.5-pro", true},
		{"vertexai_endpoint", "vertexai_endpoint", "some-model", true},
		{"openai", "openai", "gpt-4o", true},
		{"anthropic", "anthropic", "claude-3-5-sonnet", true},
		{"sagemaker not supported", "sagemaker", "llama", false},
		{"huggingface not supported", "huggingface", "meta-llama/Llama-3.3", false},
		{"unknown fails closed", "some-future-provider", "x", false},
		{"empty fails closed", "", "", false},
		{"case insensitive", "Bedrock", "m", true},
		{"whitespace trimmed", "  azure  ", "m", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SupportsNativeTools(tt.provider, tt.model); got != tt.want {
				t.Errorf("SupportsNativeTools(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestShouldUseReAct4(t *testing.T) {
	tests := []struct {
		name          string
		declared      AgentPlannerType
		react4Enabled bool
		pinned        bool
		provider      string
		want          bool
	}{
		{"flag off, native provider, react intent", AgentPlannerTypeReAct, false, false, "bedrock", false},
		{"flag on, react intent, native", AgentPlannerTypeReAct, true, false, "bedrock", true},
		{"flag on, orchestrating intent, native", AgentPlannerTypeOrchestrating, true, false, "azure", true},
		{"flag on, react intent, non-native provider", AgentPlannerTypeReAct, true, false, "sagemaker", false},
		{"flag on, react intent, unknown provider", AgentPlannerTypeReAct, true, false, "mystery", false},
		{"flag on, tool intent excluded", AgentPlannerTypeTool, true, false, "bedrock", false},
		{"flag on, custom intent excluded", AgentPlannerTypeCustom, true, false, "bedrock", false},
		{"flag on, classification intent excluded", AgentPlannerTypeClassification, true, false, "bedrock", false},
		{"flag on, conversational intent excluded", AgentPlannerTypeConversational, true, false, "bedrock", false},
		// The react_3/react_4 engine values are never declared by an agent, so
		// they must not opt into react_4 routing either.
		{"flag on, react3 engine value excluded", AgentPlannerTypeReAct3, true, false, "bedrock", false},

		// A pinned agent (NBAgentReAct4Provider, e.g. k8s_orchestrator_react4)
		// bypasses the FLAG but nothing else.
		{"pinned, flag off, native provider", AgentPlannerTypeOrchestrating, false, true, "bedrock", true},
		{"pinned, flag off, react intent, native", AgentPlannerTypeReAct, false, true, "azure", true},
		// ...the capability gate still applies: no native tool support -> ReAct3.
		{"pinned, flag off, non-native provider", AgentPlannerTypeOrchestrating, false, true, "sagemaker", false},
		{"pinned, flag off, unknown provider fails closed", AgentPlannerTypeOrchestrating, false, true, "mystery", false},
		// ...and so does the declared-intent gate.
		{"pinned, flag off, tool intent still excluded", AgentPlannerTypeTool, false, true, "bedrock", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseReAct4(tt.declared, tt.react4Enabled, tt.pinned, tt.provider, ""); got != tt.want {
				t.Errorf("shouldUseReAct4(%q, enabled=%v, pinned=%v, %q) = %v, want %v",
					tt.declared, tt.react4Enabled, tt.pinned, tt.provider, got, tt.want)
			}
		})
	}
}
