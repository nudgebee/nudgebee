package ai

import (
	"fmt"
	"nudgebee/runbook/internal/tasks/types"
	"strings"
)

// modelParamFieldName is the schema key for the optional per-node LLM model
// override. Kept in one place so the three LLM tasks (investigate, summary,
// classify) read/write the same key.
const modelParamFieldName = "model"

// modelInputSchemaProperty returns the shared Property used by LLM tasks to
// expose a per-node model picker. The frontend renders this as a dropdown
// populated by the `llm_models` options-source fetcher, which calls the
// `ai_list_models` RPC. Value is stored as `provider/model` so we don't need
// a second `provider` field on every task.
func modelInputSchemaProperty(order int) types.Property {
	return types.Property{
		Type:        types.PropertyTypeString,
		Description: "LLM model to use for this node, formatted as 'provider/model'. Leave empty to use the account default.",
		Required:    false,
		Order:       order,
		OptionsSource: &types.OptionsSource{
			Type: "llm_models",
		},
	}
}

// parseModelParam splits the optional model parameter into provider + model.
// Empty / nil input returns ("", "", nil) so callers can pass through to the
// account-default resolution path. A value with no `/` is rejected so the
// failure mode is loud instead of silently dropping the override.
func parseModelParam(raw any) (provider string, model string, err error) {
	if raw == nil {
		return "", "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", "", fmt.Errorf("model parameter must be a string, got %T", raw)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", nil
	}
	idx := strings.Index(s, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("model parameter must be in 'provider/model' form, got %q", s)
	}
	// Trim each side so `"googleai / gemini-2.5-flash"` (the label form a
	// free-solo dropdown can produce) doesn't get baked into the JSON config
	// with stray whitespace — llm-server's provider lookup is exact-match and
	// would fail with `llm model not found - googleai ` otherwise.
	provider = strings.TrimSpace(s[:idx])
	model = strings.TrimSpace(s[idx+1:])
	if provider == "" || model == "" {
		return "", "", fmt.Errorf("model parameter must be in 'provider/model' form, got %q", s)
	}
	return provider, model, nil
}
