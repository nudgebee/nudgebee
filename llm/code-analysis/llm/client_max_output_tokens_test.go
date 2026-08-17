package llm

import (
	"testing"

	"nudgebee/code-analysis-agent/config"

	"github.com/stretchr/testify/assert"
)

func cfgWith(provider, model string, maxOut int) *config.Config {
	c := &config.Config{}
	c.LLM.Provider = provider
	c.LLM.Model = model
	c.LLM.MaxOutputTokens = maxOut
	return c
}

// Bedrock's Llama schema rejects a max_gen_len above 8192 outright rather than
// clamping it, so the default ceiling makes every call fail on those models.
func TestResolveMaxOutputTokens(t *testing.T) {
	const llamaARN = "arn:aws:bedrock:us-west-2:1234:inference-profile/us.meta.llama4-maverick-17b-instruct-v1:0"

	tests := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{
			name: "bedrock llama inference profile is clamped",
			cfg:  cfgWith("bedrock", llamaARN, 0),
			want: bedrockLlamaMaxGenLen,
		},
		{
			name: "bedrock llama bare model id is clamped",
			cfg:  cfgWith("bedrock", "meta.llama3-70b-instruct-v1:0", 0),
			want: bedrockLlamaMaxGenLen,
		},
		{
			name: "provider match is case-insensitive",
			cfg:  cfgWith("Bedrock", llamaARN, 0),
			want: bedrockLlamaMaxGenLen,
		},
		{
			name: "non-llama bedrock model keeps the default",
			cfg:  cfgWith("bedrock", "anthropic.claude-3-5-sonnet-20241022-v2:0", 0),
			want: defaultMaxOutputTokens,
		},
		{
			name: "other providers keep the default",
			cfg:  cfgWith("googleai", "gemini-3-flash-preview", 0),
			want: defaultMaxOutputTokens,
		},
		{
			name: "configured value overrides the default",
			cfg:  cfgWith("googleai", "gemini-3-flash-preview", 4096),
			want: 4096,
		},
		{
			name: "a configured value below the cap is respected on llama",
			cfg:  cfgWith("bedrock", llamaARN, 2048),
			want: 2048,
		},
		{
			name: "a configured value above the cap is still clamped on llama",
			cfg:  cfgWith("bedrock", llamaARN, 32000),
			want: bedrockLlamaMaxGenLen,
		},
		{
			name: "zero and negative fall back to the default",
			cfg:  cfgWith("googleai", "gemini-3-flash-preview", -5),
			want: defaultMaxOutputTokens,
		},
		{
			name: "nil config falls back to the default",
			cfg:  nil,
			want: defaultMaxOutputTokens,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveMaxOutputTokens(tt.cfg))
		})
	}
}
