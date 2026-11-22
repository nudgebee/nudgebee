package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	choice, err := c.Choose(context.Background(), "fix it", "Classify.", []Option{{"EDIT", "change"}, {"ANSWER", ""}})
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
	choice, err = c.Choose(context.Background(), "x", "y", []Option{{Label: "CREATE"}})
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

// TestChoose_Live calls each provider whose credentials are set:
// DECISION_LIVE_CLOUDFLARE_ACCOUNT_ID + DECISION_LIVE_CLOUDFLARE_API_TOKEN,
// DECISION_LIVE_TYPESAFE_API_KEY and DECISION_LIVE_OPENAI_API_KEY.

func TestChoose_TypeSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, "jev-latest", got["model"])
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"decision":{"type":"choice","choice":"EDIT","confidence":1.0,"probabilities":{"EDIT":1.0}}},"usage":{"input_tokens":9}}`))
	}))
	defer srv.Close()
	c, err := New(Config{Provider: ProviderTypeSafe, Model: "jev-latest", APIKey: "k"})
	require.NoError(t, err)
	c.url = srv.URL // the provider's URL is fixed; point it at the test server
	choice, err := c.Choose(context.Background(), "fix it", "Classify.", []Option{{Label: "EDIT"}})
	require.NoError(t, err)
	assert.Equal(t, "EDIT", choice.Choice)
	assert.Equal(t, "jev-1.13.0", choice.Model, "the reported version is kept for logs")
}
