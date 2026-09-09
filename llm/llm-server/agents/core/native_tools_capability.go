package core

import "strings"

// nativeToolProviders is the fail-closed allow-list of LLM providers whose
// clients convert NBTool schemas into provider-native tool definitions and
// return structured tool calls (llms.ToolCall). It mirrors the provider switch
// in llm_config.go. sagemaker and huggingface are deliberately absent — their
// clients have no tool-conversion path — so agents on those providers stay on
// the ReAct3 (XML) planner. See docs/planner_react_4.md.
var nativeToolProviders = map[string]bool{
	"openai": true,
	// custom is served by getCustomLLM, which delegates to getOpenAILLM — the
	// same client, with the same tool-schema conversion. Gateways on this
	// provider (OpenRouter, LiteLLM, vLLM) speak the OpenAI tools API.
	"custom":            true,
	"bedrock":           true,
	"azure":             true,
	"googleai":          true,
	"vertexai":          true,
	"vertexai_endpoint": true,
	"anthropic":         true,
}

// SupportsNativeTools reports whether the given provider (and, in future,
// model) can run the provider-native tool-calling planner (ReAct4). It is
// fail-closed: an unknown or unlisted provider returns false so the agent
// stays on ReAct3 rather than routing to a path the provider can't serve.
//
// The model parameter is currently unused but is part of the signature so
// per-model gating (e.g. small self-hosted ollama models that accept a tools
// param but call it poorly) can be added without touching call sites.
func SupportsNativeTools(provider, model string) bool {
	_ = model
	return nativeToolProviders[strings.ToLower(strings.TrimSpace(provider))]
}

// shouldUseReAct4 is the pure (IO-free) routing decision for whether a planner
// creation should use the ReAct4 engine. It is separated from useReAct4Engine
// (which resolves provider/model from config/DB) so the routing rules can be
// unit-tested without a live LLM config.
//
// ReAct4 is selected only when all hold:
//   - the react4 flag is enabled OR the agent pins itself to ReAct4
//     (NBAgentReAct4Provider — an evaluation mirror such as
//     k8s_orchestrator_react4);
//   - the agent's DECLARED intent is ReAct or Orchestrating (the only intents
//     that resolve to a react engine — Tool/Custom/Classification/Conversational
//     never do);
//   - the resolved provider/model supports native tool calling.
//
// Note that `pinned` bypasses only the flag, never the capability gate: a
// pinned agent on a provider without native tool support still falls back to
// ReAct3 rather than failing.
func shouldUseReAct4(declared AgentPlannerType, react4Enabled, pinned bool, provider, model string) bool {
	if !react4Enabled && !pinned {
		return false
	}
	if declared != AgentPlannerTypeReAct && declared != AgentPlannerTypeOrchestrating {
		return false
	}
	return SupportsNativeTools(provider, model)
}
