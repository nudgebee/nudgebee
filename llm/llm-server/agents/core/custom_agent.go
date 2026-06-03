package core

import (
	"log/slog"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"strings"
	"sync"
	"text/template"
	"time"
)

// customAgentToolsCache is a module-level TTL cache for resolved tool lists, keyed by
// "accountId:agentName". Avoids repeated DB lookups for sub-agent tools on every request
// since nbCustomAgent instances are created fresh per invocation.
type customAgentToolsCache struct {
	mu   sync.RWMutex
	data map[string]struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}
}

const customAgentToolsCacheTTL = 30 * time.Minute

var customAgentToolsCacheInst = &customAgentToolsCache{
	data: make(map[string]struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}),
}

func (c *customAgentToolsCache) get(accountId, agentName string) ([]toolcore.NBTool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key := accountId + ":" + agentName
	item, ok := c.data[key]
	if ok && time.Now().Before(item.expiry) {
		return item.tools, true
	}
	return nil, false
}

func (c *customAgentToolsCache) set(accountId, agentName string, tools []toolcore.NBTool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[accountId+":"+agentName] = struct {
		tools  []toolcore.NBTool
		expiry time.Time
	}{tools: tools, expiry: time.Now().Add(customAgentToolsCacheTTL)}
}

func (c *customAgentToolsCache) delete(accountId, agentName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if agentName == "" {
		for k := range c.data {
			if strings.HasPrefix(k, accountId+":") {
				delete(c.data, k)
			}
		}
	} else {
		delete(c.data, accountId+":"+agentName)
	}
}

// InvalidateCustomAgentToolsCache evicts cached tool lists for a custom agent.
// Must be called after any Create / Update / Delete that changes the agent's tool list.
func InvalidateCustomAgentToolsCache(accountId, agentName string) {
	customAgentToolsCacheInst.delete(accountId, agentName)
}

type nbCustomAgent struct {
	agent     AgentDto
	accountId string
	tools     []toolcore.NBTool
}

// Compile-time check: nbCustomAgent must opt out of default-tool injection so the
// user-configured tool list (a.agent.Tools) is honored verbatim. Without this, the
// planner silently injects shell_execute / load_skills on top of the user's selection.
var _ DefaultToolsOptOut = (*nbCustomAgent)(nil)

// OptOutDefaultTools implements DefaultToolsOptOut. Custom agents are user-curated:
// the operator picks the tool list explicitly via the UI/API. The planner must not
// silently extend that scope with shell_execute or load_skills, regardless of global
// config flags. If the operator wants shell, they add `shell_execute` to the tool list.
func (a *nbCustomAgent) OptOutDefaultTools() bool {
	return true
}

func (a *nbCustomAgent) GetName() string {
	return a.agent.Name
}

func (a *nbCustomAgent) GetNameAliases() []string {
	return []string{}
}

func (a *nbCustomAgent) GetDisplayName() string {
	return a.agent.Name
}

func (a *nbCustomAgent) GetDescription() string {
	return a.agent.Description
}

func (a *nbCustomAgent) GetPlannerType() AgentPlannerType {
	return a.agent.ExecutorType
}

func (a *nbCustomAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	// Per-instance cache: warm within a single request (GetSupportedTools called by both
	// GetSystemPrompt and the executor).
	if len(a.tools) > 0 {
		return a.tools
	}
	// Module-level TTL cache shared across requests for this account+agent.
	if cached, ok := customAgentToolsCacheInst.get(a.accountId, a.agent.Name); ok {
		a.tools = cached
		return a.tools
	}

	nbTools := make([]toolcore.NBTool, 0, len(a.agent.Tools))
	if len(a.agent.Tools) == 0 {
		customAgentToolsCacheInst.set(a.accountId, a.agent.Name, nbTools)
		return nbTools
	}
	for _, tool := range a.agent.Tools {
		if tool == "" {
			continue
		}
		t, b := toolcore.GetNBTool(a.accountId, tool)
		if b {
			nbTools = append(nbTools, t)
		} else {
			agent, found := GetCustomNbAgent(ctx, a.accountId, tool, AgentStatusEnabled)
			if found {
				nbTools = append(nbTools, NewToolFromAgent(agent))
			}
		}
	}
	customAgentToolsCacheInst.set(a.accountId, a.agent.Name, nbTools)
	a.tools = nbTools
	return a.tools
}

func (a *nbCustomAgent) GetSystemPrompt(ctx *security.RequestContext, query NBAgentRequest) NBAgentPrompt {
	promptText := a.agent.SystemPrompt

	// Apply Go template rendering when the prompt contains template syntax.
	// Standard request fields (account_id, user_id, query, conversation_id) are always
	// available. SystemPromptVariables declares additional keys the user expects; they
	// default to "" so templates never fail on missing keys.
	if strings.Contains(promptText, "{{") {
		tmplData := map[string]any{
			"account_id":      query.AccountId,
			"user_id":         query.UserId,
			"query":           query.Query,
			"conversation_id": query.ConversationId,
		}
		for _, v := range a.agent.SystemPromptVariables {
			if _, exists := tmplData[v]; !exists {
				tmplData[v] = ""
			}
		}
		t, tmplErr := template.New("custom_agent").Option("missingkey=zero").Parse(promptText)
		if tmplErr != nil {
			slog.ErrorContext(ctx.GetContext(), "custom_agent: failed to parse system prompt template",
				"agent", a.agent.Name, "error", tmplErr)
		} else {
			var buf strings.Builder
			if tmplErr = t.Execute(&buf, tmplData); tmplErr != nil {
				slog.WarnContext(ctx.GetContext(), "custom_agent: failed to execute system prompt template",
					"agent", a.agent.Name, "error", tmplErr)
			} else {
				promptText = buf.String()
			}
		}
	}

	prompt := NBAgentPrompt{}
	err := common.UnmarshalJson([]byte(promptText), &prompt)
	if err != nil {
		//custom parsing
		promptMap := make(map[string]any)
		err = common.UnmarshalJson([]byte(promptText), &promptMap)
		if err != nil {
			slog.Error("agent: failed to parse system prompt", "error", err)
			prompt.Instructions = []string{
				promptText,
			}
		} else {
			if prompt.Role == "" {
				if role, ok := promptMap["role"]; ok {
					if role, ok := role.(string); ok {
						prompt.Role = role
					}
				}
			}

			if len(prompt.Instructions) == 0 {
				if instructions, ok := promptMap["instructions"]; ok {
					if instructionsAny, ok := instructions.([]any); ok {
						for _, instruction := range instructionsAny {
							if instruction, ok := instruction.(string); ok {
								prompt.Instructions = append(prompt.Instructions, instruction)
							}
						}
					} else if instructionsStrs, ok := instructions.([]string); ok {
						prompt.Instructions = append(prompt.Instructions, instructionsStrs...)
					} else if instructionsStrs, ok := instructions.(string); ok {
						prompt.Instructions = append(prompt.Instructions, instructionsStrs)
					}
				}
			}

			if len(prompt.OutputFormat) == 0 {
				if outputFormat, ok := promptMap["output_format"]; ok {
					if outputFormat, ok := outputFormat.(string); ok {
						prompt.OutputFormat = outputFormat
					} else {
						prompt.OutputFormat = "Markdown"
					}
				}
			}

			if len(prompt.ToolUsage) == 0 {
				prompt.ToolUsage = make(map[string][]string)
				if toolUsage, ok := promptMap["tool_usage"]; ok {
					if toolUsage, ok := toolUsage.(map[string]any); ok {
						for key, value := range toolUsage {
							if valueStr, ok := value.(string); ok {
								prompt.ToolUsage[key] = []string{valueStr}
							} else if values, ok := value.([]any); ok {
								for _, v := range values {
									if v, ok := v.(string); ok {
										prompt.ToolUsage[key] = append(prompt.ToolUsage[key], v)
									}
								}
							} else if values, ok := value.([]string); ok {
								prompt.ToolUsage[key] = values
							}
						}
					}
				}
			}

			if len(prompt.Constraints) == 0 {
				if constraintsAny, ok := promptMap["constraints"]; ok {
					if constraints, ok := constraintsAny.([]any); ok {
						for _, constraint := range constraints {
							if constraint, ok := constraint.(string); ok {
								prompt.Constraints = append(prompt.Constraints, constraint)
							}
						}
					} else if constraints, ok := constraintsAny.([]string); ok {
						prompt.Constraints = constraints
					} else if constraints, ok := constraintsAny.(string); ok {
						prompt.Constraints = []string{constraints}
					}
				}

			}

			if len(prompt.Schema) == 0 {
				if schemaAny, ok := promptMap["schema"]; ok {
					if schemas, ok := schemaAny.([]any); ok {
						for _, schema := range schemas {
							if schemaStr, ok := schema.(string); ok {
								prompt.Schema = append(prompt.Schema, schemaStr)
							}
						}
					} else if schema, ok := schemaAny.([]string); ok {
						prompt.Schema = schema
					} else if schema, ok := schemaAny.(string); ok {
						prompt.Schema = []string{schema}
					}
				}
			}

			if len(prompt.Examples) == 0 {
				if examplesAny, ok := promptMap["examples"]; ok {
					if examples, ok := examplesAny.([]any); ok {
						for _, example := range examples {
							if schemaStr, ok := example.(string); ok {
								ex := NBAgentPromptExample{}
								ex.Answer = schemaStr
								prompt.Examples = append(prompt.Examples, ex)
							}
						}
					}
				}
			}
		}
	}

	// When the global config promotes a ReWoo agent to ReAct3, the user's prompt may contain
	// ReWoo-specific format instructions (XML plan steps) that would conflict with the
	// thought/action/observation loop. Append a corrective note so the planner's base
	// prompt format takes precedence over any stale format instructions in the user prompt.
	if a.agent.ExecutorType == AgentPlannerTypeReWoo &&
		(config.Config.LlmServerRewooToReact3Enabled || config.Config.LlmServerReAct3Enabled) {
		prompt.Instructions = append(prompt.Instructions,
			"Use the ReAct thought/action/observation format. Do not generate an XML step plan.")
	}

	tools := a.GetSupportedTools(ctx)
	if len(tools) == 0 {
		return prompt
	}

	// populate tool usage in the prompt
	for _, tool := range tools {
		existingUsage, ok := prompt.ToolUsage[tool.Name()]
		if !ok {
			prompt.ToolUsage[tool.Name()] = []string{}
			existingUsage = prompt.ToolUsage[tool.Name()]
		}
		existingUsage = append(existingUsage, tool.Description())
		prompt.ToolUsage[tool.Name()] = existingUsage
	}

	isMultiToolSupport := false
	for _, t := range tools {
		if _, ok := t.(toolcore.NBMultiCommandTool); ok {
			isMultiToolSupport = true
		}
	}

	if !isMultiToolSupport {
		return prompt
	}

	prompt1, err := UpdatePromptForMultiCommandTool(query, tools, prompt)
	if err != nil {
		slog.Error("agent: failed to get prompt for multi command tool", "error", err)
	} else {
		prompt = prompt1
	}

	return prompt
}
