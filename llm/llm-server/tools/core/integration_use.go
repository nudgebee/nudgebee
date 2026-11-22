package core

import (
	"strings"

	"nudgebee/llm/security"
)

// UsesIntegration reports whether a call of tool reaches one of the tenant's
// integrations: an MCP integration tool, or a tool configured from
// integrations (config source integration, ticket or ticket_all).
func UsesIntegration(ctx *security.RequestContext, tool NBTool) bool {
	if tool == nil {
		return false
	}
	if ImplTypeFor(tool) == ToolImplTypeMCP {
		return true
	}
	configured, ok := tool.(NBToolConfig)
	if !ok {
		return false
	}
	switch configured.ConfigSchema(ctx).ConfigSource {
	case ToolConfigSourceIntegration, ToolConfigSourceTicket, ToolConfigSourceTicketAll:
		return true
	}
	return false
}

// BoundIntegrationType is the stored type of the integration a call of tool
// uses with config, the configuration the call resolved: "mcp" for an MCP
// tool, else the configuration's own type (a ticket_all configuration names
// it), else its schema's config type. "" when the call resolved none.
func BoundIntegrationType(tool NBTool, config ToolConfig) string {
	if tool != nil && ImplTypeFor(tool) == ToolImplTypeMCP {
		return "mcp"
	}
	for _, v := range config.Values {
		if v.Name == "type" && strings.TrimSpace(v.Value) != "" {
			return strings.TrimSpace(v.Value)
		}
	}
	return strings.TrimSpace(config.Schema.ConfigType)
}
