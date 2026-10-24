package integrations

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/runbook/internal/tasks/testutils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The path and query of a webhook or API-key URL are the credential, so the
// step never writes them to a log line or an error.
const urlCanary = "Zx9-canary-7Qw"

func canaryURL(base string) string {
	return base + "/services/T0/" + urlCanary + "?token=" + urlCanary
}

func runLoggedHTTPStep(t *testing.T, ctx context.Context, params map[string]any) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	taskCtx := testutils.NewTestTaskContextWithContext(ctx, "t", "a", "u", logger)
	_, err := (&HttpTask{}).Execute(taskCtx, params)
	return buf.String(), err
}

func TestHttpRequestTask_KeepsURLCredentialOutOfLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logs, err := runLoggedHTTPStep(t, context.Background(), map[string]any{"url": canaryURL(server.URL), "method": "POST", "body": "{}"})

	require.NoError(t, err)
	assert.NotContains(t, logs, urlCanary)
	assert.Contains(t, logs, server.URL, "the host is still logged")
}

func TestHttpRequestTask_KeepsURLCredentialOutOfAFailedRequest(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachable := server.URL
	server.Close()

	logs, err := runLoggedHTTPStep(t, context.Background(), map[string]any{"url": canaryURL(unreachable)})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), urlCanary)
	assert.Contains(t, err.Error(), unreachable+`"`, "the error still names the host it could not reach")
	assert.NotContains(t, logs, urlCanary)
}

func TestHttpRequestTask_StoppedRequestStillReadsAsCanceled(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runLoggedHTTPStep(t, ctx, map[string]any{"url": canaryURL(server.URL)})

	require.ErrorIs(t, err, context.Canceled)
	assert.NotContains(t, err.Error(), urlCanary)
}

func TestHttpRequestTask_KeepsProxyCredentialOutOfErrors(t *testing.T) {
	_, err := runLoggedHTTPStep(t, context.Background(), map[string]any{
		"url":       "https://example.com",
		"proxy_url": "http://user:" + urlCanary + "@proxy\x7f.example.com",
	})

	require.Error(t, err)
	assert.False(t, strings.Contains(err.Error(), urlCanary), err.Error())
}
