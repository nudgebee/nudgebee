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
	"sync/atomic"
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
	touched               atomic.Int64
	metadataNotFound      []string
	metadataChanged       bool
	datasourceOwners      map[string]string // datasource key → owning agent id
	ownerErr              error
	upsertAgentID         string
	tenants               map[string]string // account id → owning tenant
}

func (f *fakeStore) AccountTenant(ctx context.Context, accountID, agentType string) (string, error) {
	return f.tenants[accountID], f.err
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
) (bool, string, string, string, error) {
	return f.allowed, f.fallback, "k8s", "", f.err
}

func (f *fakeStore) GetAgentStatus(ctx context.Context, accountID, agentType string) (connected bool, wsEnabled bool, fallbackURL string, prometheusClusterLabel string, err error) {
	return f.allowed, f.allowed, f.fallback, f.prometheuClusterLabel, f.err
}

func (f *fakeStore) UpdateRelayConnectionStatus(ctx context.Context, accountID, agentType string, relayConnected bool, sessionStart time.Time) (bool, string, error) {
	return false, "", f.err
}

func (f *fakeStore) TouchRelaySession(ctx context.Context, accountID, agentType string) error {
	f.touched.Add(1)
	return f.err
}

func (f *fakeStore) UpdateAgentVersion(ctx context.Context, accountID, agentType, version, commit, buildTime, protocolVersion string) error {
	return f.err
}

func (f *fakeStore) UpdateDatasourceHealth(ctx context.Context, accountID, agentType string, datasources map[string]any) error {
	return f.err
}

func (f *fakeStore) UpsertAgentDatasources(ctx context.Context, accountID, agentType, agentID string, datasources []db.AgentDatasource) error {
	f.upsertAgentID = agentID
	return f.err
}

func (f *fakeStore) GetDatasourceAgentID(ctx context.Context, accountID, datasourceKey string) (string, error) {
	return f.datasourceOwners[datasourceKey], f.ownerErr
}

func (f *fakeStore) GetWorkspaceToolConfig(ctx context.Context, accountID, integrationType, configName string) ([]db.WorkspaceConfigValue, error) {
	return nil, f.err
}

func (f *fakeStore) UpdateDatasourceMetadata(ctx context.Context, accountID, agentType string, metadata map[string]map[string]any) ([]string, bool, error) {
	return f.metadataNotFound, f.metadataChanged, f.err
}

func (f *fakeStore) QueryProxyDatasources(ctx context.Context, accountID string) ([]db.ProxyDatasource, error) {
	return nil, f.err
}

// fakeRPCClient implements the minimal Call signature.
type fakeRPCClient struct {
	resp         []byte
	err          error
	calls        []rpcCall
	deliverLocal bool
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

func (f fakeRPCClient) InstanceID() string { return "fake-instance" }

func (f fakeRPCClient) DeliverLocal(corrID string, body []byte) bool {
	return f.deliverLocal
}

func (f fakeRPCClient) AbandonCh(corrID string) <-chan struct{} { return nil }

func (f fakeRPCClient) Close() {
}

type fakeEnsurer struct{}

func (f *fakeEnsurer) EnsureTenant(ctx context.Context, id string) error { return nil }
func (f *fakeEnsurer) EnsureTenantForAgentType(ctx context.Context, id, agentType string) error {
	return nil
}

// --- tests ---

func setupRouter(store db.AgentStore, rpcClient fakeRPCClient) *gin.Engine {
	return setupRouterWithLogs(store, rpcClient, io.Discard)
}

func setupRouterWithLogs(store db.AgentStore, rpcClient fakeRPCClient, logs io.Writer) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// no auth middleware
	cfg := &config.Config{}
	cfg.HTTP.WriteTimeout = 50 * time.Millisecond
	cfg.RabbitMQ.ExchangeName = "exch"
	tracer := tracer.NewTracerProvider().Tracer("test")
	meter := noop.NewMeterProvider().Meter("test")
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fakeTopo := &fakeEnsurer{}

	if err := metrics.Init(meter); err != nil {
		logger.Error("metrics init failed", "error", err)
	}
	r.POST("/request", NewRequestHandler(store, rpcClient, fakeTopo, nil, cfg, nil, &tracer, &meter, logger))
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

func TestRequestHandler_AgentNotConnected(t *testing.T) {
	store := &fakeStore{allowed: false}
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

	require.Equal(t, 503, w.Code)
	var body map[string][]map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(503), body["errors"][0]["code"])
	require.Equal(t, "agent not connected", body["errors"][0]["message"])
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

// A failed call must not write credential-bearing params to the logs: a shell
// command can embed a password, and MCP calls carry auth_credentials.
func TestRequestHandler_FailedCallDoesNotLogCredentials(t *testing.T) {
	cases := []struct {
		name   string
		action string
		params map[string]interface{}
		rpcErr error
		msg    string
	}{
		{
			name:   "command on timeout",
			action: "pod_script_run_enricher",
			params: map[string]interface{}{"command": "psql postgresql://app:s3cr3t-pw@db.example.com:5432/app -c 'select 1'", "pod_name": "p1"},
			rpcErr: context.DeadlineExceeded,
			msg:    "RPC timeout",
		},
		{
			name:   "mcp credentials on error",
			action: "mcp_request",
			params: map[string]interface{}{"url": "https://mcp.example.com", "auth_credentials": map[string]interface{}{"bearer_token": "s3cr3t-pw"}},
			rpcErr: fmt.Errorf("broker unavailable"),
			msg:    "RPC error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			router := setupRouterWithLogs(&fakeStore{allowed: true}, fakeRPCClient{err: tc.rpcErr}, &logs)

			input := models.ExternalActionRequest{
				Body:      models.ActionRequestBody{AccountID: "acct1", ActionName: tc.action, ActionParams: tc.params},
				RequestID: "rid123",
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(jsonMust(input)))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			require.Contains(t, logs.String(), tc.msg)
			require.Contains(t, logs.String(), "action_name="+tc.action)
			require.NotContains(t, logs.String(), "s3cr3t-pw")
		})
	}
}

// Read-only lookups keep their params in the log: they say which query or
// resource the agent failed to answer.
func TestRequestHandler_FailedLookupLogsParams(t *testing.T) {
	var logs bytes.Buffer
	router := setupRouterWithLogs(&fakeStore{allowed: true}, fakeRPCClient{err: context.DeadlineExceeded}, &logs)

	input := models.ExternalActionRequest{
		Body: models.ActionRequestBody{
			AccountID:    "acct1",
			ActionName:   "get_resource",
			ActionParams: map[string]interface{}{"resource_type": "configmaps"},
		},
		RequestID: "rid123",
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBuffer(jsonMust(input)))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, 504, w.Code)
	require.Contains(t, logs.String(), "configmaps")
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

// A caller acting for one tenant must not reach another tenant's agent, even
// with the shared secret: X-TENANT-ID has to own the account.
func TestRequestHandler_TenantMustOwnAccount(t *testing.T) {
	ar := models.AgentResponse{StatusCode: 200, RequestID: "rid", Data: string(jsonMust(models.HTTPResponse{StatusCode: 200}))}

	for _, tc := range []struct {
		name     string
		tenant   string
		body     string
		wantCode int
	}{
		{"own account", "tenant-a", `{"body":{"account_id":"11111111-1111-1111-1111-11111111111a"}}`, 200},
		{"own account, header case differs", "TENANT-A", `{"body":{"account_id":"11111111-1111-1111-1111-11111111111a"}}`, 200},
		{"other tenant's account (nested)", "tenant-a", `{"body":{"account_id":"22222222-2222-2222-2222-22222222222b"}}`, 403},
		{"other tenant's account (top level)", "tenant-a", `{"account_id":"22222222-2222-2222-2222-22222222222b"}`, 403},
		{"unknown account", "tenant-a", `{"account_id":"33333333-3333-3333-3333-33333333333c"}`, 403},
		{"no tenant header (internal caller)", "", `{"account_id":"22222222-2222-2222-2222-22222222222b"}`, 200},
		{"malformed account id", "tenant-a", `{"account_id":"not-a-uuid"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{allowed: true, tenants: map[string]string{"11111111-1111-1111-1111-11111111111a": "tenant-a", "22222222-2222-2222-2222-22222222222b": "tenant-b"}}
			router := setupRouter(store, fakeRPCClient{resp: jsonMust(ar)})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/request", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.tenant != "" {
				req.Header.Set("X-TENANT-ID", tc.tenant)
			}
			router.ServeHTTP(w, req)

			require.Equal(t, tc.wantCode, w.Code, w.Body.String())
		})
	}
}

// Direct-mode MCP is served before any agent check; the tenant check must run
// before it too.
func TestRequestHandler_TenantCheckPrecedesDirectMCP(t *testing.T) {
	store := &fakeStore{allowed: true, tenants: map[string]string{"22222222-2222-2222-2222-22222222222b": "tenant-b"}}
	router := setupRouter(store, fakeRPCClient{})

	body := `{"body":{"account_id":"22222222-2222-2222-2222-22222222222b","action_name":"mcp_request","action_params":{"connection_mode":"direct","url":"http://127.0.0.1:1/mcp"}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TENANT-ID", "tenant-a")
	router.ServeHTTP(w, req)

	require.Equal(t, 403, w.Code, w.Body.String())
}

func TestRequestHandler_TenantLookupErrorFailsClosed(t *testing.T) {
	store := &fakeStore{allowed: true, err: fmt.Errorf("db gone")}
	router := setupRouter(store, fakeRPCClient{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/request", bytes.NewBufferString(`{"account_id":"11111111-1111-1111-1111-11111111111a"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TENANT-ID", "tenant-a")
	router.ServeHTTP(w, req)

	require.Equal(t, 500, w.Code)
}
