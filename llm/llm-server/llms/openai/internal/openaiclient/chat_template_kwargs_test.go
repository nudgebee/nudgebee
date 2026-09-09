package openaiclient

import (
	"encoding/json"
	"strings"
	"testing"
)

// chat_template_kwargs is a vLLM extension: it must serialize when set and be
// absent entirely when not, so servers that don't know the field are untouched.
func TestChatRequest_ChatTemplateKwargsSerialization(t *testing.T) {
	t.Run("omitted when unset", func(t *testing.T) {
		b, err := json.Marshal(&ChatRequest{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "chat_template_kwargs") {
			t.Errorf("field must be omitted when unset: %s", b)
		}
	})

	t.Run("serialized when set", func(t *testing.T) {
		b, err := json.Marshal(&ChatRequest{
			Model:              "m",
			ChatTemplateKwargs: map[string]any{"enable_thinking": false},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"chat_template_kwargs":{"enable_thinking":false}`) {
			t.Errorf("want chat_template_kwargs in body, got: %s", b)
		}
	})
}

// vLLM reasoning parsers stream chain-of-thought as "reasoning"; deepseek uses
// "reasoning_content". Both must land in ReasoningContent, otherwise a
// reasoning-only turn looks identical to a hung one.
func TestStreamedPayload_ReasoningFieldAliases(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"vllm reasoning", `{"choices":[{"delta":{"reasoning":"thinking hard"}}]}`, "thinking hard"},
		{"deepseek reasoning_content", `{"choices":[{"delta":{"reasoning_content":"deep thought"}}]}`, "deep thought"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p StreamedChatResponsePayload
			if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
				t.Fatal(err)
			}
			d := p.Choices[0].Delta
			got := d.ReasoningContent
			if got == "" {
				got = d.Reasoning
			}
			if got != tc.want {
				t.Errorf("reasoning delta = %q, want %q", got, tc.want)
			}
		})
	}
}
