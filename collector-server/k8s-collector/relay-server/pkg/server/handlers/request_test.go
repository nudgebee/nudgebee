package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/models"
	"nudgebee/relay-server/pkg/server/metrics"

	"go.opentelemetry.io/otel/metric/noop"
	tracer "go.opentelemetry.io/otel/trace/noop"
)

// --- fakes ---

type fakeStore struct {
	allowed               bool
	fallback              string
	prometheuClusterLabel string
	err                   error
}

func (f *fakeStore) IsWSEnabled(ctx context.Context, acct, agentType string) (bool, string, error) {
	return f.allowed, f.fallback, f.err
}

func (f *fakeStore) IsAgentConnected(ctx context.Context, acct, agentType string) (bool, error) {
	return f.allowed, f.err
}

func (f *fakeStore) ValidateAgent(
	ctx context.Context,
	accessKey, secret string,
) (bool, string, string, error) {
	return f.allowed, f.fallback, "k8s", f.err
}

func (f *fakeStore) GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusClusterLabel string, err error) {
	return f.allowed, f.allowed, f.fallback, f.prometheuClusterLabel, f.err
}

func (f *fakeStore) UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) error {
	return f.err
}

func (f *fakeStore) UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error {
	return f.err
}

func (f *fakeStore) UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error {
	return f.err
}

func (f *fakeStore) UpsertAgentDatasources(ctx context.Context, accountID, agentType string, datasources []db.AgentDatasource) error {
	return f.err
}

func (f *fakeStore) GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]db.WorkspaceConfigValue, error) {
	return nil, f.err
}

func (f *fakeStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) error {
	return f.err
}

func (f *fakeStore) QueryProxyDatasources(ctx context.Context, accountID string) ([]db.ProxyDatasource, error) {
	return nil, f.err
}

// fakeRPCClient implements the minimal Call signature.
type fakeRPCClient struct {
	resp  []byte
	err   error
	calls []rpcCall
}

type rpcCall struct {
	Exchange   string
	RoutingKey string
	Payload    []byte
	RequestID  string
}

func (f fakeRPCClient) Call(
	ctx context.Context,
	exchange, routingKey string,
	payload []byte,
	requestID string,
) ([]byte, error) {
	f.calls = append(f.calls, rpcCall{exchange, routingKey, payload, requestID}) //nolint:staticcheck
	return f.resp, f.err
}

func (f fakeRPCClient) Close() {
}

type fakeEnsurer struct{}

func (f *fakeEnsurer) EnsureTenant(ctx context.Context, id string) error { return nil }
func (f *fakeEnsurer) EnsureTenantForAgentType(ctx context.Context, id, agentType string) error {
	return nil
}

// --- tests ---

func setupRouter(store db.AgentStore, rpcClient fakeRPCClient) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// no auth middleware
	cfg := &config.Config{}
	cfg.HTTP.WriteTimeout = 50 * time.Millisecond
	cfg.RabbitMQ.ExchangeName = "exch"
	tracer := tracer.NewTracerProvider().Tracer("test")
	meter := noop.NewMeterProvider().Meter("test")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fakeTopo := &fakeEnsurer{}

	if err := metrics.Init(meter); err != nil {
		logger.Error("metrics init failed", "error", err)
	}
	r.POST("/request", NewRequestHandler(store, rpcClient, fakeTopo, cfg, nil, &tracer, &meter, logger))
	return r
}

func TestRequestHandler_InvalidJSON(t *testing.T) {
	store := &fakeStore{allowed: true}
	rpc := fakeRPCClient{}
	router := setupRouter(store, rpc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 400, w.Code)
	var body map[string][]map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, float64(400), body["errors"][0]["code"])
	require.Equal(t, "invalid JSON", body["errors"][0]["message"])
}

func TestRequestHandler_MissingAccountID(t *testing.T) {
	store := &fakeStore{allowed: true}
	rpc := fakeRPCClient{}
	router := setupRouter(store, rpc)

	// send JSON without body.account_id
	input := models.ExternalActionRequest{
		Body: models.ActionRequestBody{
			AccountID: "",
		},
	}
	b, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 400, w.Code)
	var body map[string][]map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(400), body["errors"][0]["code"])
	require.Equal(t, "missing account_id", body["errors"][0]["message"])
}

func TestRequestHandler_StoreError(t *testing.T) {
	store := &fakeStore{err: fmt.Errorf("db gone")}
	rpc := fakeRPCClient{}
	router := setupRouter(store, rpc)

	input := models.ExternalActionRequest{
		Body: models.ActionRequestBody{AccountID: "acct1"},
	}
	b, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 500, w.Code)
	var body map[string][]map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(500), body["errors"][0]["code"])
	require.Equal(t, "internal server error", body["errors"][0]["message"])
}

func TestRequestHandler_RPCError(t *testing.T) {
	store := &fakeStore{allowed: true}
	rpc := fakeRPCClient{err: context.DeadlineExceeded}
	router := setupRouter(store, rpc)

	input := models.ExternalActionRequest{
		Body:      models.ActionRequestBody{AccountID: "acct1"},
		RequestID: "rid123",
	}
	b, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 504, w.Code)
	var body map[string][]map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(504), body["errors"][0]["code"])
	require.Equal(t, "timeout waiting for agent", body["errors"][0]["message"])
}

func TestRequestHandler_Success(t *testing.T) {
	store := &fakeStore{allowed: true}
	// Simulate an AgentResponse containing base64 body
	hr := models.HTTPResponse{
		StatusCode: 202,
		Header:     map[string][]string{"Content-Type": {"text/plain"}},
		Body:       base64.StdEncoding.EncodeToString([]byte("OK")),
	}
	ar := models.AgentResponse{
		StatusCode: 200,
		RequestID:  "rid123",
		Data:       string(jsonMust(hr)),
	}
	respBytes := jsonMust(ar)

	rpc := fakeRPCClient{resp: respBytes}
	router := setupRouter(store, rpc)

	input := models.ExternalActionRequest{
		Body:      models.ActionRequestBody{AccountID: "acct1"},
		RequestID: "rid123",
	}
	b, _ := json.Marshal(input)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(b))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	var body models.AgentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, 200, body.StatusCode)
	require.Equal(t, "rid123", body.RequestID)
}

// helper: marshal v or panic
func jsonMust(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}
