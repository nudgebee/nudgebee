package core

// Opt-in adapter/prompt/parser replay. No infrastructure tools or DB writes.
// Input packets and credentials are supplied locally, never committed.
import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"nudgebee/llm/llms/googleai"
	nbprompts "nudgebee/llm/prompts"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

func TestClaimCritiqueLiveReplay(t *testing.T) {
	input := os.Getenv("CLAIM_CRITIQUE_REPLAY_INPUT")
	if input == "" {
		t.Skip("set CLAIM_CRITIQUE_REPLAY_INPUT to opt in to paid inference")
	}
	key, endpoint, model := os.Getenv("CLAIM_CRITIQUE_REPLAY_KEY"), os.Getenv("CLAIM_CRITIQUE_REPLAY_ENDPOINT"), os.Getenv("CLAIM_CRITIQUE_REPLAY_MODEL")
	output := os.Getenv("CLAIM_CRITIQUE_REPLAY_OUTPUT")
	require.NotEmpty(t, key)
	require.NotEmpty(t, endpoint)
	require.NotEmpty(t, model)
	require.NotEmpty(t, output)
	type replayCase struct {
		ID     string          `json:"id"`
		Packet json.RawMessage `json:"packet"`
	}
	raw, err := os.ReadFile(input)
	require.NoError(t, err)
	var cases []replayCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)
	client, err := googleai.New(context.Background(), googleai.WithAPIKey(key), googleai.WithBaseURL(endpoint), googleai.WithDefaultModel(model))
	require.NoError(t, err)
	prompt, err := nbprompts.GetPromptStrict(context.Background(), nbprompts.PromptReactClaimCritiquer, "")
	require.NoError(t, err)
	type replayResult struct {
		ID       string                `json:"id"`
		Status   string                `json:"status"`
		Decision string                `json:"decision"`
		Feedback string                `json:"feedback"`
		Seconds  float64               `json:"seconds"`
		Response *llms.ContentResponse `json:"response"`
	}
	results := []replayResult{}
	for _, c := range cases {
		require.LessOrEqual(t, len(c.Packet), claimCritiqueInputLimit)
		var packet struct {
			Observations []claimObservation `json:"observations"`
		}
		require.NoError(t, json.Unmarshal(c.Packet, &packet))
		ctx, cancel := context.WithTimeout(context.Background(), claimCritiqueTimeout)
		start := time.Now()
		response, callErr := client.GenerateContent(ctx, []llms.MessageContent{
			llms.TextParts(llms.ChatMessageTypeSystem, prompt),
			llms.TextParts(llms.ChatMessageTypeHuman, string(c.Packet)),
		}, llms.WithTemperature(0), llms.WithJSONMode(), llms.WithMaxTokens(8192))
		cancel()
		result := replayResult{ID: c.ID, Seconds: time.Since(start).Seconds(), Response: response, Status: "generation_error"}
		if callErr == nil {
			evaluation := evaluateClaimResponse(response, len(packet.Observations))
			result.Status, result.Decision, result.Feedback = evaluation.Status, evaluation.Decision, evaluation.Feedback
		}
		results = append(results, result)
		encoded, marshalErr := json.MarshalIndent(results, "", "  ")
		require.NoError(t, marshalErr)
		require.NoError(t, os.WriteFile(output, encoded, 0600))
		t.Logf("case=%s status=%s decision=%s seconds=%.2f", result.ID, result.Status, result.Decision, result.Seconds)
	}
}
