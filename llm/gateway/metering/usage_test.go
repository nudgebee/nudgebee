package metering

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tier(t schemas.BifrostServiceTier) *schemas.BifrostServiceTier { return &t }

func TestNewEvent_FullUsage(t *testing.T) {
	ev := NewEvent(EventInput{
		Provider:   schemas.Anthropic,
		Model:      "claude-haiku-4-5",
		Method:     "POST",
		Path:       "/v1/messages",
		Streaming:  false,
		StatusCode: 200,
		LatencyMS:  1498,
		Headers:    map[string]string{"request-id": "req_abc"},
		Usage: &schemas.BifrostPassthroughUsage{
			ServiceTier: tier("default"),
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens:     14,
				CompletionTokens: 7,
				TotalTokens:      21,
			},
		},
	})

	assert.NotEmpty(t, ev.ID)
	assert.False(t, ev.CreatedAt.IsZero())
	assert.Equal(t, "anthropic", ev.Provider)
	assert.Equal(t, "claude-haiku-4-5", ev.Model)
	assert.Equal(t, "POST", ev.Method)
	assert.Equal(t, "/v1/messages", ev.Path)
	assert.False(t, ev.Streaming)
	assert.Equal(t, 200, ev.StatusCode)
	assert.Equal(t, int64(1498), ev.LatencyMS)
	assert.Equal(t, "req_abc", ev.RequestID)
	assert.Equal(t, 14, ev.InputTokens)
	assert.Equal(t, 7, ev.OutputTokens)
	assert.Equal(t, 21, ev.TotalTokens)
	assert.Equal(t, "default", ev.ServiceTier)
	assert.Zero(t, ev.CacheReadTokens)
	assert.Zero(t, ev.CacheWriteTokens)
}

func TestNewEvent_CacheTokens(t *testing.T) {
	// Mirrors the verified cache-read row: prompt cached, cache_read populated.
	ev := NewEvent(EventInput{
		Provider:   schemas.Anthropic,
		Model:      "claude-sonnet-4-6",
		Method:     "POST",
		Path:       "/v1/messages",
		StatusCode: 200,
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens: 4011,
				TotalTokens:  4015,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens:  4003,
					CachedWriteTokens: 0,
				},
			},
		},
	})
	assert.Equal(t, 4003, ev.CacheReadTokens)
	assert.Equal(t, 0, ev.CacheWriteTokens)
	assert.Equal(t, 8, ev.InputTokens, "input_tokens holds only the fresh (uncached) prompt tokens")
}

func TestNewEvent_InputExcludesCacheReadAndWrite(t *testing.T) {
	// Anthropic-shaped: 200 fresh + 800 read + 100 written = 1100 prompt tokens.
	ev := NewEvent(EventInput{
		Provider: schemas.Anthropic,
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens:     1100,
				CompletionTokens: 50,
				TotalTokens:      1150,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens:  800,
					CachedWriteTokens: 100,
				},
			},
		},
	})
	assert.Equal(t, 200, ev.InputTokens)
	assert.Equal(t, 800, ev.CacheReadTokens)
	assert.Equal(t, 100, ev.CacheWriteTokens)
	assert.Equal(t, 1150, ev.TotalTokens, "total is unchanged")
}

func TestNewEvent_InputNeverNegative(t *testing.T) {
	ev := NewEvent(EventInput{
		Provider: schemas.OpenAI,
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens: 10,
				TotalTokens:  10,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens: 12,
				},
			},
		},
	})
	assert.Equal(t, 0, ev.InputTokens)
}

func TestNewEvent_CostNoLongerDoubleBillsCache(t *testing.T) {
	// $3/M input, $0.30/M cached read: 200 fresh + 800 cached should cost 200*3 + 800*0.3.
	ev := NewEvent(EventInput{
		Provider: schemas.Anthropic,
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{
				PromptTokens: 1000,
				TotalTokens:  1000,
				PromptTokensDetails: &schemas.ChatPromptTokensDetails{
					CachedReadTokens: 800,
				},
			},
		},
	})
	p := &Pricer{}
	p.cur.Store(&catalog{builtin: map[string]modelPrice{"m": {Input: 3, CachedInput: 0.3}}})
	got := p.CostUSD("", "m", ev.InputTokens, ev.OutputTokens, ev.CacheReadTokens, ev.CacheWriteTokens)
	assert.InDelta(t, (200*3+800*0.3)/1e6, got, 1e-12)
}

func TestNewEvent_StreamingUsageFromFinalChunk(t *testing.T) {
	ev := NewEvent(EventInput{
		Provider:   schemas.Anthropic,
		Model:      "claude-haiku-4-5",
		Method:     "POST",
		Path:       "/v1/messages",
		Streaming:  true,
		StatusCode: 200,
		Usage: &schemas.BifrostPassthroughUsage{
			LLMUsage: &schemas.BifrostLLMUsage{PromptTokens: 11, CompletionTokens: 13, TotalTokens: 24},
		},
	})
	assert.True(t, ev.Streaming)
	assert.Equal(t, 11, ev.InputTokens)
	assert.Equal(t, 13, ev.OutputTokens)
	assert.Equal(t, 24, ev.TotalTokens)
}

func TestNewEvent_NilUsage_AuditRow(t *testing.T) {
	// GET /v1/models and error paths carry no usage — the row is still recorded
	// with zero tokens for traffic/audit.
	ev := NewEvent(EventInput{
		Provider:   schemas.Anthropic,
		Method:     "GET",
		Path:       "/v1/models",
		StatusCode: 200,
		Usage:      nil,
	})
	assert.Equal(t, "GET", ev.Method)
	assert.Zero(t, ev.InputTokens)
	assert.Zero(t, ev.OutputTokens)
	assert.Zero(t, ev.TotalTokens)
	assert.Empty(t, ev.ServiceTier)
	assert.NotEmpty(t, ev.ID) // still a valid, recordable row
}

func TestNewEvent_UsageWithoutLLMUsage(t *testing.T) {
	// Defensive: PassthroughUsage present but LLMUsage nil (e.g. non-LLM passthrough)
	// must not panic and must yield zero token counts.
	require.NotPanics(t, func() {
		ev := NewEvent(EventInput{
			Provider:   schemas.Gemini,
			StatusCode: 200,
			Usage:      &schemas.BifrostPassthroughUsage{ServiceTier: tier("flex")},
		})
		assert.Zero(t, ev.TotalTokens)
		assert.Equal(t, "flex", ev.ServiceTier)
	})
}

func TestNewEvent_IDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := NewEvent(EventInput{Provider: schemas.OpenAI, StatusCode: 200}).ID
		require.False(t, seen[id], "duplicate id generated: %s", id)
		seen[id] = true
	}
}

func TestRequestIDFromHeaders(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		want string
	}{
		{"anthropic request-id", map[string]string{"request-id": "req_1"}, "req_1"},
		{"openai x-request-id", map[string]string{"x-request-id": "req_2"}, "req_2"},
		{"case-insensitive", map[string]string{"Request-Id": "req_3"}, "req_3"},
		{"absent", map[string]string{"content-type": "application/json"}, ""},
		{"nil map", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, requestIDFromHeaders(tc.in))
		})
	}
}
