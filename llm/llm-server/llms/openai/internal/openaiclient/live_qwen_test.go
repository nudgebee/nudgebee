package openaiclient_test

// Live end-to-end check of the vendored client against the Vertex-hosted Qwen
// endpoint whose empty trailer deltas produced phantom nameless tool calls.
// Opt-in only: skipped unless QWEN_LIVE_BASE_URL and QWEN_LIVE_TOKEN are set.
//
//	QWEN_LIVE_BASE_URL=https://<dns>/v1beta1/projects/<n>/locations/<r>/endpoints/<ep> \
//	QWEN_LIVE_TOKEN=$(gcloud auth print-access-token) \
//	go test ./llms/openai/... -run TestLive_QwenStreamingToolCalls -v

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms"

	openaifork "nudgebee/llm/llms/openai"
)

func TestLive_QwenStreamingToolCalls(t *testing.T) {
	base, token := os.Getenv("QWEN_LIVE_BASE_URL"), os.Getenv("QWEN_LIVE_TOKEN")
	if base == "" || token == "" {
		t.Skip("QWEN_LIVE_BASE_URL / QWEN_LIVE_TOKEN not set")
	}

	opts := []openaifork.Option{
		openaifork.WithToken(token),
		openaifork.WithBaseURL(base),
		openaifork.WithModel("Qwen/Qwen3.6-35B-A3B-FP8"),
	}
	// QWEN_LIVE_DISABLE_THINKING=1 exercises the llm_disable_thinking path:
	// chat_template_kwargs {"enable_thinking": false} must suppress the model's
	// chain-of-thought while leaving tool calling intact.
	if os.Getenv("QWEN_LIVE_DISABLE_THINKING") != "" {
		opts = append(opts, openaifork.WithChatTemplateKwargs(map[string]any{"enable_thinking": false}))
	}
	llm, err := openaifork.New(opts...)
	if err != nil {
		t.Fatal(err)
	}

	tools := []llms.Tool{{
		Type: "function",
		Function: &llms.FunctionDefinition{
			Name:        "kubectl_execute",
			Description: "Run a read-only kubectl command against the cluster",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
					"reason":  map[string]any{"type": "string"},
				},
				"required": []string{"command"},
			},
		},
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	resp, err := llm.GenerateContent(ctx,
		[]llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem,
				"You are an SRE agent. Use the provided tools; do not answer from memory."),
			llms.TextParts(llms.ChatMessageTypeHuman,
				"The web-app pod in namespace-231 keeps restarting. Check pod status and recent events with tool calls."),
		},
		llms.WithTools(tools),
		llms.WithMaxTokens(4000),
		llms.WithStreamingFunc(func(context.Context, []byte) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("no choices")
	}

	t.Logf("reasoning chars: %d", len(resp.Choices[0].ReasoningContent))
	if os.Getenv("QWEN_LIVE_DISABLE_THINKING") != "" && resp.Choices[0].ReasoningContent != "" {
		t.Errorf("thinking disabled but model still returned %d reasoning chars",
			len(resp.Choices[0].ReasoningContent))
	}

	tcs := resp.Choices[0].ToolCalls
	if len(tcs) == 0 {
		t.Fatal("no tool calls assembled")
	}
	for i, tc := range tcs {
		if tc.FunctionCall == nil || tc.FunctionCall.Name == "" {
			t.Errorf("tool call %d has no name (phantom): id=%s", i, tc.ID)
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(tc.FunctionCall.Arguments), &parsed); err != nil {
			t.Errorf("tool call %d (%s) has unparseable arguments %q: %v",
				i, tc.FunctionCall.Name, tc.FunctionCall.Arguments, err)
		}
		t.Logf("tool call %d: %s(%s)", i, tc.FunctionCall.Name, tc.FunctionCall.Arguments)
	}
}
