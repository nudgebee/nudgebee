package decision

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	_, err := New(Config{Provider: ProviderCloudflare, APIKey: "k", Endpoint: "acct"})
	assert.ErrorContains(t, err, "model")
	_, err = New(Config{Provider: ProviderCloudflare, Model: "clef", Endpoint: "acct"})
	assert.ErrorContains(t, err, "API token")
	_, err = New(Config{Provider: ProviderCloudflare, Model: "clef", APIKey: "k"})
	assert.ErrorContains(t, err, "account id")
	_, err = New(Config{Provider: "googleai", Model: "m", APIKey: "k", Endpoint: "acct"})
	assert.ErrorContains(t, err, "unknown provider")

	c, err := New(Config{Provider: ProviderCloudflare, Model: "clef-flash", APIKey: "k", Endpoint: "acct"})
	require.NoError(t, err)
	assert.Equal(t, "https://api.cloudflare.com/client/v4/accounts/acct/ai/run/@cf/cloudflare/clef-flash", c.url, "an account id becomes the Workers AI URL")
	c, err = New(Config{Provider: ProviderCloudflare, Model: "clef", APIKey: "k", Endpoint: "https://gateway.example.com/ai/run/"})
	require.NoError(t, err)
	assert.Equal(t, "https://gateway.example.com/ai/run/@cf/cloudflare/clef", c.url, "a full base URL is kept")

	c, err = New(Config{Provider: ProviderTypeSafe, Model: "jev-latest", APIKey: "k"})
	require.NoError(t, err)
	assert.Equal(t, typesafeURL, c.url, "TypeSafe needs no endpoint")
	c, err = New(Config{Provider: ProviderOpenAI, Model: "gpt-6-luna", APIKey: "k", Endpoint: "acct"})
	require.NoError(t, err)
	assert.Equal(t, openaiURL, c.url, "a leftover Cloudflare account id is ignored")
}

func TestIsProvider(t *testing.T) {
	assert.True(t, IsProvider("cloudflare"))
	assert.True(t, IsProvider(" Cloudflare "))
	assert.True(t, IsProvider("typesafe"))
	assert.True(t, IsProvider("openai"))
	assert.False(t, IsProvider("googleai"))
	assert.False(t, IsProvider(""))

	assert.True(t, IsDecisionOnlyProvider("cloudflare"))
	assert.True(t, IsDecisionOnlyProvider("TypeSafe"))
	assert.False(t, IsDecisionOnlyProvider("openai"), "OpenAI also serves chat models")

	assert.True(t, IsDecisionModel("openai", "gpt-6-luna"))
	assert.False(t, IsDecisionModel("openai", "gpt-4o"))
	assert.True(t, IsDecisionModel("typesafe", "jev-latest"))
	assert.False(t, IsDecisionModel("googleai", "gpt-6-luna"))
}

func TestChoose_Live(t *testing.T) {
	options := []Option{
		{"ANSWER", "The user wants information, an explanation, a diagnosis or status, with no change requested."},
		{"EDIT", "The user wants to change or repair the automation that is open."},
		{"CREATE", "The user wants to build a new automation from scratch."},
	}
	cases := []Config{}
	if acct, token := os.Getenv("DECISION_LIVE_CLOUDFLARE_ACCOUNT_ID"), os.Getenv("DECISION_LIVE_CLOUDFLARE_API_TOKEN"); acct != "" && token != "" {
		for _, m := range Models[ProviderCloudflare] {
			cases = append(cases, Config{Provider: ProviderCloudflare, Model: m, APIKey: token, Endpoint: acct})
		}
	}
	if key := os.Getenv("DECISION_LIVE_TYPESAFE_API_KEY"); key != "" {
		cases = append(cases, Config{Provider: ProviderTypeSafe, Model: "jev-latest", APIKey: key})
	}
	if key := os.Getenv("DECISION_LIVE_OPENAI_API_KEY"); key != "" {
		cases = append(cases, Config{Provider: ProviderOpenAI, Model: "gpt-6-luna", APIKey: key})
	}
	if len(cases) == 0 {
		t.Skip("no decision-model credentials set")
	}
	for _, cfg := range cases {
		t.Run(cfg.Provider+"/"+cfg.Model, func(t *testing.T) {
			c, err := New(cfg)
			require.NoError(t, err)
			choice, err := c.Choose(context.Background(), "Replace the approver with another teammate.",
				"An automation is open. Which action does the user want?", options)
			require.NoError(t, err)
			require.False(t, choice.Refused)
			assert.Equal(t, "EDIT", choice.Choice)
			require.NotNil(t, choice.Confidence)
			t.Logf("choice=%s confidence=%.3f probabilities=%v model=%s tokens=%d", choice.Choice, *choice.Confidence, choice.Probabilities, choice.Model, choice.InputTokens)
		})
	}
}

func TestTruncate_KeepsCharactersWhole(t *testing.T) {
	assert.Equal(t, "abc", truncate([]byte("abc"), 5))
	assert.Equal(t, "ab…", truncate([]byte("abcdef"), 2))
	got := truncate([]byte("aé"), 2) // "é" is two bytes; cutting at 2 would split it
	assert.Equal(t, "a…", got)
}
