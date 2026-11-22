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

func TestChoose_OpenAI(t *testing.T) {
	var got map[string]any
	reply := `{"model":"gpt-6-luna","answers":[{"type":"choice","name":"decision","choice":"EDIT","probabilities":[{"value":"ANSWER","probability":0.05},{"value":"EDIT","probability":0.95}],"confidence":0.93}],"usage":{"input_tokens":20}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		assert.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	c, err := New(Config{Provider: ProviderOpenAI, Model: "gpt-6-luna", APIKey: "k"})
	require.NoError(t, err)
	c.url = srv.URL // the provider's URL is fixed; point it at the test server

	choice, err := c.Choose(context.Background(), "fix it", "Classify.", []Option{{"ANSWER", ""}, {"EDIT", "change"}})
	require.NoError(t, err)
	assert.Equal(t, "EDIT", choice.Choice)
	require.NotNil(t, choice.Confidence)
	assert.InDelta(t, 0.93, *choice.Confidence, 1e-9)
	assert.Equal(t, map[string]float64{"ANSWER": 0.05, "EDIT": 0.95}, choice.Probabilities)
	assert.Equal(t, 20, choice.InputTokens)

	// Request shape: input, one named choice question, options in the caller's order.
	assert.Equal(t, "fix it", got["input"])
	q := got["questions"].([]any)[0].(map[string]any)
	assert.Equal(t, "decision", q["name"])
	choices := q["choices"].([]any)
	assert.Equal(t, "ANSWER", choices[0].(map[string]any)["value"])
	assert.Equal(t, "change", choices[1].(map[string]any)["description"])

	reply = `{"model":"gpt-6-luna","answers":[{"type":"refusal","name":"decision"}]}`
	choice, err = c.Choose(context.Background(), "x", "y", []Option{{Label: "EDIT"}})
	require.NoError(t, err, "a refusal is an answer, not an error")
	assert.True(t, choice.Refused)
	assert.Empty(t, choice.Choice)

	reply = `{"answers":[{"type":"choice","name":"other","choice":"EDIT"}]}`
	_, err = c.Choose(context.Background(), "x", "y", nil)
	assert.ErrorContains(t, err, "no answer")
}
