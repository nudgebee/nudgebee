package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/config"
	"nudgebee/services/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

// serveLLM points config at a stub llm-server for the duration of one test.
func serveLLM(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	previous := config.Config.LLMServerEndpoint
	config.Config.LLMServerEndpoint = server.URL
	t.Cleanup(func() {
		config.Config.LLMServerEndpoint = previous
		server.Close()
	})
}

// alertLabels deliberately carries no alertname: isClusterScopedAlert short-circuits
// on an empty alert name, so the extractor is reached without needing a database.
func alertLabels() map[string]string {
	return map[string]string{"container": "ecr-proxy-server", "cluster": "dev"}
}

// A failed extractor call must leave a record on the event. Without one, the
// failure is indistinguishable from an alert that never needed the agent, and the
// presence-keyed alreadyTried guard in enrichEventsWithSubjectResolution lets the
// next stage immediately repeat the call ChatCompletion has already retried.
func TestResolveSubjectNameViaAgent_LabelsCallFailure(t *testing.T) {
	serveLLM(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// An `errors` array makes ChatCompletion return an error on the first attempt.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{{"message": "prompt exceeds model context limit"}},
		})
	})

	labels := alertLabels()
	name := ResolveSubjectNameViaAgent(newTestRequestContext(), "acct-1", "stderr spike", "", "", labels)

	assert.Equal(t, "", name)
	assert.Equal(t, "error", labels["nb_llm_match"])
	// The error text must not reach the event — event labels render in the UI's
	// Alert-labels evidence table (#35130).
	assert.NotContains(t, labels["nb_llm_match"], "context limit")
	assert.Empty(t, labels["service"], "a failed call must not set a service name")
}

// A completed call that carries no answer is a distinct outcome from a decline
// ("not_found") and from a hard failure, and gets its own value.
func TestResolveSubjectNameViaAgent_LabelsEmptyResponse(t *testing.T) {
	serveLLM(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"status": "COMPLETED", "response": []string{}},
		})
	})

	labels := alertLabels()
	name := ResolveSubjectNameViaAgent(newTestRequestContext(), "acct-1", "stderr spike", "", "", labels)

	assert.Equal(t, "", name)
	assert.Equal(t, "empty", labels["nb_llm_match"])
	assert.Empty(t, labels["service"])
}

// newTestRequestContext builds a context without touching the database. The
// tenant-scoped constructors resolve the tenant's account ids over the metastore
// and return a nil SecurityContext when it is unreachable, which then nil-panics
// inside the function under test.
func newTestRequestContext() *security.RequestContext {
	return security.NewRequestContext(
		context.Background(),
		security.NewSecurityContextForSuperAdmin(),
		slog.Default(), nil, nil,
	)
}
