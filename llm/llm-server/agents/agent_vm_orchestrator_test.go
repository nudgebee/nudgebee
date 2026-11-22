package agents

import (
	"context"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/prompts"
	"nudgebee/llm/services_server"

	"github.com/stretchr/testify/assert"
)

func TestVMAccountRoutesBeforeProvider(t *testing.T) {
	for _, provider := range []string{"SelfHosted", "AWS", "Azure", "GCP"} {
		if got := debugAgentForAccount(provider, "vm"); got != AgentVMOrchestratorName {
			t.Errorf("provider %s routed to %s", provider, got)
		}
	}
	if got := debugAgentForAccount("AWS", "cloud"); got != "aws_orchestrator" {
		t.Fatalf("AWS cloud account routed to %s", got)
	}
	agent, ok := core.GetNBAgent(nil, AgentVMOrchestratorName, "", "")
	if !ok || agent.GetPlannerType() != core.AgentPlannerTypeOrchestrating {
		t.Fatal("VM orchestrator registration missing")
	}
	if _, err := prompts.GetPromptStrict(context.Background(), prompts.PromptVMLean, ""); err != nil {
		t.Fatalf("VM prompt: %v", err)
	}
}

func TestFormatLinkedServices(t *testing.T) {
	assert.Empty(t, formatLinkedServices(nil))

	got := formatLinkedServices([]services_server.VMServiceLink{
		{VM: "vm-a", Service: "/docker/api", VMServiceCodeLink: services_server.VMServiceCodeLink{Repo: "https://gitlab.com/org/api", Branch: "release"}},
		{VM: "vm-a", Service: "/system.slice/web.service", VMServiceCodeLink: services_server.VMServiceCodeLink{Repo: "https://github.com/org/web"}},
	})
	assert.Contains(t, got, "- vm-a, /docker/api, https://gitlab.com/org/api, release")
	assert.Contains(t, got, "- vm-a, /system.slice/web.service, https://github.com/org/web, default branch")
}
