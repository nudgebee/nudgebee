package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomAgentSharedCritiquerIsOptIn(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		enabled bool
	}{
		{name: "missing config defaults off", config: map[string]any{}, enabled: false},
		{name: "explicit false", config: map[string]any{"enable_shared_critiquer": false}, enabled: false},
		{name: "explicit true", config: map[string]any{"enable_shared_critiquer": true}, enabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &nbCustomAgent{agent: AgentDto{Config: tt.config}}
			assert.Equal(t, tt.enabled, agent.CritiqueEnabled())
		})
	}
}

// A custom agent's prompt is stored as JSON, and tool_usage is omitempty. Any
// agent authored outside the UI — through the ai_create_agent API, a migration,
// or by hand — normally has no tool_usage key at all, which unmarshals to a nil
// map. GetSystemPrompt then writes each supported tool into that map, so the
// first tool turned the agent into a panic and killed the conversation.
func TestCustomAgentPromptToolUsageIsWritableWhenAbsent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"no tool_usage key", `{"role":"r","instructions":["i"]}`},
		{"explicit null", `{"role":"r","tool_usage":null}`},
		{"empty object", `{"role":"r","tool_usage":{}}`},
		{"already populated", `{"role":"r","tool_usage":{"existing":["desc"]}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prompt NBAgentPrompt
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &prompt))

			// Mirrors the guard in GetSystemPrompt.
			if prompt.ToolUsage == nil {
				prompt.ToolUsage = make(map[string][]string)
			}

			assert.NotPanics(t, func() {
				prompt.ToolUsage["some_tool"] = append(prompt.ToolUsage["some_tool"], "a description")
			})
			assert.Equal(t, []string{"a description"}, prompt.ToolUsage["some_tool"])
		})
	}
}

// Without the guard the same write panics, which is the failure this protects
// against — asserted directly so the test fails if the map is ever assumed
// non-nil again.
func TestNilToolUsageWriteWouldPanic(t *testing.T) {
	var prompt NBAgentPrompt
	require.NoError(t, json.Unmarshal([]byte(`{"role":"r"}`), &prompt))
	require.Nil(t, prompt.ToolUsage)

	assert.Panics(t, func() {
		prompt.ToolUsage["some_tool"] = []string{"a description"}
	})
}

func TestCustomAgentGetSystemPromptToolUsageFallback(t *testing.T) {
	t.Run("string tool_usage is appended to instructions", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"tool_usage": "Use kubectl with caution"
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		assert.Contains(t, prompt.Instructions, "do task")
		assert.Contains(t, prompt.Instructions, "Use kubectl with caution")
		assert.Empty(t, prompt.ToolUsage["toolUsage"])
	})

	t.Run("legacy toolUsage map key is appended to instructions without phantom tool", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"tool_usage": {
						"toolUsage": "Always verify inputs first"
					}
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		assert.Contains(t, prompt.Instructions, "do task")
		assert.Contains(t, prompt.Instructions, "Always verify inputs first")
		_, hasPhantom := prompt.ToolUsage["toolUsage"]
		assert.False(t, hasPhantom, "phantom toolUsage header should not be present in ToolUsage map")
	})

	t.Run("legitimate tool usage map is preserved in ToolUsage", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"tool_usage": {
						"kubectl_tool": "Only run get or describe commands"
					}
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		assert.Equal(t, []string{"Only run get or describe commands"}, prompt.ToolUsage["kubectl_tool"])
	})
}

func TestCustomAgentGetSystemPromptExamplesFallback(t *testing.T) {
	t.Run("string examples are parsed into NBAgentPromptExample", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"examples": "User: What pods are running?\nAssistant: Pod A, Pod B"
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		require.Len(t, prompt.Examples, 1)
		assert.Equal(t, "User: What pods are running?\nAssistant: Pod A, Pod B", prompt.Examples[0].Answer)
	})

	t.Run("string slice examples are parsed into NBAgentPromptExample", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"examples": ["Example 1", "Example 2"]
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		require.Len(t, prompt.Examples, 2)
		assert.Equal(t, "Example 1", prompt.Examples[0].Answer)
		assert.Equal(t, "Example 2", prompt.Examples[1].Answer)
	})

	t.Run("structured object examples are parsed properly", func(t *testing.T) {
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"role": "tester",
					"instructions": ["do task"],
					"examples": [
						{"question": "How to restart?", "answer": "Run rollout restart", "explanation": "Clean restart"}
					]
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		require.Len(t, prompt.Examples, 1)
		assert.Equal(t, "How to restart?", prompt.Examples[0].Question)
		assert.Equal(t, "Run rollout restart", prompt.Examples[0].Answer)
		assert.Equal(t, "Clean restart", prompt.Examples[0].Explanation)
	})

	t.Run("answer_steps survive the fallback path", func(t *testing.T) {
		// A string tool_usage fails the typed unmarshal, forcing the fallback.
		agent := &nbCustomAgent{
			agent: AgentDto{
				Name: "test_agent",
				SystemPrompt: `{
					"tool_usage": "use kubectl",
					"examples": [
						{"question": "Why is the pod down?", "answer_steps": [{"tool": "kubectl", "input": "get events"}]}
					]
				}`,
			},
		}

		prompt := agent.GetSystemPrompt(nil, NBAgentRequest{})
		require.Len(t, prompt.Examples, 1)
		assert.Equal(t, "Why is the pod down?", prompt.Examples[0].Question)
		require.Len(t, prompt.Examples[0].AnswerSteps, 1)
		assert.Equal(t, "kubectl", prompt.Examples[0].AnswerSteps[0].Tool)
	})
}

func TestCustomAgentThinkingLevel(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{name: "absent", config: nil, want: ThinkingLevelLow},
		{name: "low", config: map[string]any{"thinking_level": "LOW"}, want: ThinkingLevelLow},
		{name: "trimmed", config: map[string]any{"thinking_level": " medium "}, want: ThinkingLevelMedium},
		{name: "unknown", config: map[string]any{"thinking_level": "enthusiastic"}, want: ThinkingLevelLow},
		{name: "wrong type", config: map[string]any{"thinking_level": 2}, want: ThinkingLevelLow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agent := &nbCustomAgent{agent: AgentDto{Config: tc.config}}
			assert.Equal(t, tc.want, ResolveAgentThinkingLevel(agent))
		})
	}

	assert.Empty(t, ResolveAgentThinkingLevel(&MockAgent{}),
		"agents without the optional capability must preserve default resolution")
}

// The exact system_prompt the custom-agent wizard (CreateAgentNew.jsx) saves today.
// Before the fix it rendered a phantom "**toolUsage**" tool and dropped the examples.
func TestCustomAgentWizardPayloadRendersWithoutPhantomTool(t *testing.T) {
	agent := &nbCustomAgent{
		agent: AgentDto{
			Name: "wizard_agent",
			SystemPrompt: `{"role":"SRE helper","instructions":["check pods first"],"constraints":["read only"],` +
				`"tool_usage":{"toolUsage":"use kubectl for pod status"},"output_format":"Markdown",` +
				`"examples":"Question: why is pod down?\nAnswer: it was OOMKilled"}`,
		},
	}
	req := NBAgentRequest{Query: "q"}
	prompt := agent.GetSystemPrompt(nil, req)

	for _, planner := range []AgentPlannerType{AgentPlannerTypeReAct, AgentPlannerTypeCustom} {
		rendered, err := GetPromptTemplate(prompt, req, planner).Format(map[string]any{"input": req.Query})
		require.NoError(t, err)
		assert.NotContains(t, rendered, "toolUsage", planner)
		assert.Contains(t, rendered, "use kubectl for pod status", planner)
		assert.Contains(t, rendered, "it was OOMKilled", planner)
	}
}

// Literal "{{" in user text (GitHub Actions secrets, Helm values) must render
// verbatim. Before, it failed the prompt render, leaving the agent with an empty
// system prompt.
func TestCustomAgentPromptWithTemplateBracesRendersVerbatim(t *testing.T) {
	cases := []struct{ name, systemPrompt, want string }{
		{"example", `{"role":"r","examples":"Question: deploy?\nAnswer: use ${{ secrets.TOKEN }}"}`, "use ${{ secrets.TOKEN }}"},
		{"tool_usage", `{"role":"r","tool_usage":{"toolUsage":"set image {{ .Values.image }}"}}`, "set image {{ .Values.image }}"},
		{"instruction", `{"role":"r","instructions":["read ${{ secrets.TOKEN }}"]}`, "read ${{ secrets.TOKEN }}"},
		{"nested json untouched", `{"role":"r","instructions":["send {\"a\":{\"b\":1}}"]}`, `send {"a":{"b":1}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &nbCustomAgent{agent: AgentDto{Name: "a", SystemPrompt: tc.systemPrompt}}
			req := NBAgentRequest{Query: "q"}
			prompt := agent.GetSystemPrompt(nil, req)
			for _, planner := range []AgentPlannerType{AgentPlannerTypeReAct, AgentPlannerTypeCustom} {
				rendered, err := GetPromptTemplate(prompt, req, planner).Format(map[string]any{"history": ""})
				require.NoError(t, err, planner)
				assert.Contains(t, rendered, "You are r", planner)
				assert.Contains(t, rendered, tc.want, planner)
			}
		})
	}
}
