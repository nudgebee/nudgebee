package agents

import (
	"fmt"
	"strings"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// MissingIntegrationError signals that an explicitly-mentioned agent (@<name>)
// cannot run because every tool it depends on requires an integration config
// and the account has none installed.
type MissingIntegrationError struct {
	AgentName    string
	MissingTools []string
}

func (e *MissingIntegrationError) Error() string {
	return fmt.Sprintf("agent %q requires an integration; configure one of: %s",
		e.AgentName, strings.Join(e.MissingTools, ", "))
}

// configResolver matches toolcore.ListToolConfigs and exists so tests can
// inject a stub without going through the DB-backed cache.
type configResolver func(ctx *security.RequestContext, accountId string, tool toolcore.NBTool) ([]toolcore.ToolConfig, error)

var defaultConfigResolver configResolver = toolcore.ListToolConfigs

// EnsureAgentIntegrations returns nil when `agent` has at least one usable
// tool for the given account, and *MissingIntegrationError otherwise.
//
// "Usable" means: the tool does not implement toolcore.NBToolConfig (no
// integration required) OR it does and at least one config is installed.
// Agents with zero supported tools always pass — they have no integration
// dependency to validate.
func EnsureAgentIntegrations(ctx *security.RequestContext, agent core.NBAgent, accountId string) error {
	return ensureAgentIntegrations(ctx, agent, accountId, defaultConfigResolver)
}

func ensureAgentIntegrations(ctx *security.RequestContext, agent core.NBAgent, accountId string, resolve configResolver) error {
	tools := agent.GetSupportedTools(ctx)
	if len(tools) == 0 {
		return nil
	}

	var missing []string
	for _, tool := range tools {
		if _, requiresConfig := tool.(toolcore.NBToolConfig); !requiresConfig {
			return nil
		}
		configs, err := resolve(ctx, accountId, tool)
		if err != nil {
			ctx.GetLogger().Warn("integration_precheck: list tool configs failed; allowing through",
				"agent", agent.GetName(), "tool", tool.Name(), "error", err)
			return nil
		}
		if len(configs) > 0 {
			return nil
		}
		missing = append(missing, tool.Name())
	}

	return &MissingIntegrationError{
		AgentName:    agent.GetName(),
		MissingTools: missing,
	}
}
