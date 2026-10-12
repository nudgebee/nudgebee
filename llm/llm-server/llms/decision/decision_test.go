package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
}

func TestIsProvider(t *testing.T) {
	assert.True(t, IsProvider("cloudflare"))
	assert.True(t, IsProvider(" Cloudflare "))
	assert.False(t, IsProvider("googleai"))
	assert.False(t, IsProvider(""))
}

func TestChoose(t *testing.T) {
	var got map[string]any
	reply := `{"result":{"model":"clef-1.0","answers":{"decision":{"type":"choice","choice":"EDIT","probabilities":{"EDIT":0.9,"ANSWER":0.1},"confidence":0.85}},"usage":{"input_tokens":12}},"success":true}`
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		assert.Equal(t, "/@cf/cloudflare/clef", r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	c, err := New(Config{Provider: ProviderCloudflare, Model: "clef", APIKey: "k", Endpoint: srv.URL})
	require.NoError(t, err)

	choice, err := c.Choose(context.Background(), "fix it", "Classify.", map[string]string{"EDIT": "change", "ANSWER": ""})
	require.NoError(t, err)
	assert.Equal(t, "EDIT", choice.Choice)
	require.NotNil(t, choice.Confidence)
	assert.InDelta(t, 0.85, *choice.Confidence, 1e-9)
	assert.Equal(t, "clef-1.0", choice.Model)
	assert.Equal(t, 12, choice.InputTokens)

	// Request shape: one choice question; an empty criterion is sent as null.
	q := got["questions"].(map[string]any)["decision"].(map[string]any)
	assert.Equal(t, "choice", q["type"])
	criteria := q["criteria"].(map[string]any)
	assert.Nil(t, criteria["ANSWER"])
	assert.Equal(t, "change", criteria["EDIT"])
	assert.Equal(t, "fix it", got["state"])

	// A bare body and an absent confidence are accepted.
	reply = `{"answers":{"decision":{"choice":"CREATE"}}}`
	choice, err = c.Choose(context.Background(), "x", "y", map[string]string{"CREATE": ""})
	require.NoError(t, err)
	assert.Equal(t, "CREATE", choice.Choice)
	assert.Nil(t, choice.Confidence, "absent confidence stays nil")
	assert.Equal(t, "clef", choice.Model, "falls back to the configured model")

	reply = `{"result":{"answers":{}},"success":false}`
	_, err = c.Choose(context.Background(), "x", "y", nil)
	assert.ErrorContains(t, err, "success=false")

	// A Workers AI error has success=false and no result; its message is kept.
	reply = `{"result":null,"success":false,"errors":[{"code":5007,"message":"No such model"}]}`
	_, err = c.Choose(context.Background(), "x", "y", nil)
	assert.ErrorContains(t, err, "No such model")

	reply = `{"answers":{}}`
	_, err = c.Choose(context.Background(), "x", "y", nil)
	assert.ErrorContains(t, err, "no answer")

	status, reply = http.StatusUnauthorized, `{"error":"bad key"}`
	_, err = c.Choose(context.Background(), "x", "y", nil)
	assert.ErrorContains(t, err, "HTTP 401")
}

// TestChoose_Live calls Cloudflare. It runs only when DECISION_LIVE_CLOUDFLARE_ACCOUNT_ID
// and DECISION_LIVE_CLOUDFLARE_API_TOKEN are set.
func TestChoose_Live(t *testing.T) {
	acct, token := os.Getenv("DECISION_LIVE_CLOUDFLARE_ACCOUNT_ID"), os.Getenv("DECISION_LIVE_CLOUDFLARE_API_TOKEN")
	if acct == "" || token == "" {
		t.Skip("no Cloudflare credentials set")
	}
	criteria := map[string]string{
		"ANSWER": "The user wants information, an explanation, a diagnosis or status, with no change requested.",
		"EDIT":   "The user wants to change or repair the automation that is open.",
		"CREATE": "The user wants to build a new automation from scratch.",
	}
	for _, model := range []string{"clef", "clef-flash"} {
		t.Run(model, func(t *testing.T) {
			c, err := New(Config{Provider: ProviderCloudflare, Model: model, APIKey: token, Endpoint: acct})
			require.NoError(t, err)
			choice, err := c.Choose(context.Background(), "Replace the approver with another teammate.",
				"An automation is open. Which action does the user want?", criteria)
			require.NoError(t, err)
			assert.Equal(t, "EDIT", choice.Choice)
			require.NotNil(t, choice.Confidence)
			t.Logf("%s: choice=%s confidence=%.3f probabilities=%v model=%s tokens=%d", model, choice.Choice, *choice.Confidence, choice.Probabilities, choice.Model, choice.InputTokens)
		})
	}
}

func TestTruncate_KeepsCharactersWhole(t *testing.T) {
	assert.Equal(t, "abc", truncate([]byte("abc"), 5))
	assert.Equal(t, "ab…", truncate([]byte("abcdef"), 2))
	got := truncate([]byte("aé"), 2) // "é" is two bytes; cutting at 2 would split it
	assert.Equal(t, "a…", got)
}
