package promforager

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/integrations/promclient"
)

func TestTransport(t *testing.T) {
	var gotAccount, gotAction, gotDS, gotAgent string
	var gotParams map[string]any
	orig := executeProxy
	defer func() { executeProxy = orig }()
	executeProxy = func(_ context.Context, account, action, ds string, params map[string]any, _ int, agent string) (map[string]any, error) {
		gotAccount, gotAction, gotDS, gotAgent, gotParams = account, action, ds, agent, params
		return map[string]any{
			"status_code": float64(200),
			"header":      map[string]any{"Content-Type": []any{"application/json"}},
			"body":        base64.StdEncoding.EncodeToString([]byte(`{"status":"success"}`)),
		}, nil
	}

	cfg, err := promclient.NewPrometheusUserConfig(map[string]string{promclient.PrometheusURLKey: "http://10.0.0.5:9090"})
	require.NoError(t, err)
	cfg.ProxyTransport = NewTransport("acc-1", "ds-1", "proxy-b")

	resp, err := cfg.DoGet(context.Background(), "/api/v1/query", map[string][]string{"query": {"up"}})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	assert.Equal(t, `{"status":"success"}`, string(body[:n]))

	assert.Equal(t, "acc-1", gotAccount)
	assert.Equal(t, "http_request", gotAction)
	assert.Equal(t, "ds-1", gotDS)
	assert.Equal(t, "proxy-b", gotAgent)
	// Path only: the forager owns the base URL and rejects absolute URLs.
	assert.Equal(t, "/api/v1/query?query=up", gotParams["url"])
	assert.Equal(t, "GET", gotParams["method"])
}

func TestTransport_Errors(t *testing.T) {
	orig := executeProxy
	defer func() { executeProxy = orig }()

	executeProxy = func(context.Context, string, string, string, map[string]any, int, string) (map[string]any, error) {
		return nil, assert.AnError
	}
	_, _, _, err := NewTransport("a", "d", "")(context.Background(), "GET", "/x", nil, nil)
	require.Error(t, err)

	executeProxy = func(context.Context, string, string, string, map[string]any, int, string) (map[string]any, error) {
		return map[string]any{"body": ""}, nil
	}
	_, _, _, err = NewTransport("a", "d", "")(context.Background(), "GET", "/x", nil, nil)
	require.Error(t, err, "a response without status_code must not be treated as success")
}

func TestTransportPropagatesContext(t *testing.T) {
	orig := executeProxy
	defer func() { executeProxy = orig }()

	var got context.Context
	executeProxy = func(ctx context.Context, _, _, _ string, _ map[string]any, _ int, _ string) (map[string]any, error) {
		got = ctx
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := NewTransport("acct", "ds", "")(ctx, "GET", "/api/v1/query", nil, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, ctx, got)
}
