package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nbctl's exact documents for its seven workspace commands, as pinned in
// nudgebee/nbctl cmd/workspace_contract_test.go (#120). If nbctl changes them,
// this test and nbctl's contract test must change together.
var nbctlWorkspaceDocuments = map[string]struct {
	document  string
	variables map[string]any
}{
	"metrics_list_names": {`query MetricsListNames($accountId: String!) {
  metrics_list_names(request: {account_id: $accountId}) {
    metric
  }
}`, map[string]any{"accountId": "acct"}},
	"metrics_list_labels": {`query MetricsLabelList($accountId: String!, $metricName: String!) {
  metrics_list_labels(request: {account_id: $accountId, metric: $metricName}) {
    label
  }
}`, map[string]any{"accountId": "acct", "metricName": "up"}},
	"metrics_list_label_values": {`query MetricsLabelValueList($accountId: String!, $labelName: String!) {
  metrics_list_label_values(request: {account_id: $accountId, label: $labelName}) {
    value
  }
}`, map[string]any{"accountId": "acct", "labelName": "job"}},
	"metrics_list": {`query MetricsQuery($request: FetchMetricsRequest!) {
  metrics_list(request: $request) {
    results
  }
}`, map[string]any{"request": map[string]any{"account_id": "acct", "queries": map[string]any{"query": "sum(up)"}, "instant": false, "start_time": 1.0, "end_time": 2.0, "step_interval": 300.0}}},
	"logs_list_labels": {`query FetchLogLabels($request: FetchLogLabelRequest!) {
  logs_list_labels(request: $request) {
    label
    kind
    field
    data_type
    attributes
  }
}`, map[string]any{"request": map[string]any{"account_id": "acct", "request": map[string]any{"query": "start=1&end=2", "index": "logs-*"}, "fields_only": true}}},
	"logs_list_label_values": {`query FetchLogLabelValues($accountId: String!, $labelName: String!, $query: String!, $index: String!) {
  logs_list_label_values(request: {account_id: $accountId, label_name: $labelName, request: {index: $index, query: $query}}) {
    value
  }
}`, map[string]any{"accountId": "acct", "labelName": "namespace", "query": "start=1&end=2", "index": "logs-*"}},
	"logs_list": {`query FetchLogs($request: FetchLogRequest!) {
  logs_list(request: $request) {
    logs {
      timestamp
      severity
      message
      labels
    }
    query
    provider
    suggestion
    truncated
    total
    total_relation
    series
    series_note
    partial
    aggregations_raw
    total_raw
  }
}`, map[string]any{"request": map[string]any{"account_id": "acct", "query": `{namespace="shop"}`, "limit": 5000.0, "offset": 0.0, "request": map[string]any{"index": "logs-*", "query_type": "dsl"}, "include_raw": true}}},
}

func TestParseNbctlOperationContract(t *testing.T) {
	for field, doc := range nbctlWorkspaceDocuments {
		t.Run(field, func(t *testing.T) {
			op, err := parseNbctlOperation(doc.document, doc.variables)
			require.NoError(t, err)
			assert.Equal(t, field, op.Field)
			_, allowed := workspaceNbctlActions[op.Field]
			assert.True(t, allowed, "every nbctl workspace command is on the allowlist")
			request, ok := op.Args["request"].(map[string]any)
			require.True(t, ok, "the action input is a request object")
			assert.Equal(t, "acct", request["account_id"])
		})
	}
	op, err := parseNbctlOperation(nbctlWorkspaceDocuments["logs_list_label_values"].document, nbctlWorkspaceDocuments["logs_list_label_values"].variables)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"account_id": "acct", "label_name": "namespace", "request": map[string]any{"index": "logs-*", "query": "start=1&end=2"}}, op.Args["request"])
}

func TestParseNbctlOperationRefusesOtherShapes(t *testing.T) {
	cases := map[string]string{
		"mutation":          `mutation { tickets_create(request: {account_id: "a"}) { id } }`,
		"two fields":        `query { metrics_list_names(request: {account_id: "a"}) { metric } logs_list_labels(request: {account_id: "a"}) { label } }`,
		"alias":             `query { x: metrics_list_names(request: {account_id: "a"}) { metric } }`,
		"fragment":          `fragment F on X { a } query { metrics_list_names(request: {account_id: "a"}) { ...F } }`,
		"missing variable":  `query Q($a: String!) { metrics_list_names(request: {account_id: $a}) { metric } }`,
		"two operations":    `query A { metrics_list_names(request: {account_id: "a"}) { metric } } query B { logs_list_labels(request: {account_id: "a"}) { label } }`,
		"unterminated text": `query { metrics_list_names(request: {account_id: "a}) { metric } }`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseNbctlOperation(doc, map[string]any{})
			assert.Error(t, err)
		})
	}
}

type nbctlServiceCall struct {
	path    string
	headers http.Header
	body    map[string]any
}

// nbctlProxySetup points the proxy at a fake services-server and returns the
// calls it receives.
func nbctlProxySetup(t *testing.T) *[]nbctlServiceCall {
	t.Helper()
	var calls []nbctlServiceCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		calls = append(calls, nbctlServiceCall{path: r.URL.Path, headers: r.Header.Clone(), body: body})
		_, _ = w.Write([]byte(`[{"metric":"up"}]`))
	}))
	t.Cleanup(srv.Close)
	orig := config.Config
	t.Cleanup(func() { config.Config = orig })
	config.Config.ServiceEndpoint = srv.URL
	config.Config.ServiceApiServerToken = "action-token"
	config.Config.LlmServerJwtSecret = "test-secret"
	config.Config.LlmServerWorkspaceNbctlEnabled = true
	config.Config.LlmServerWorkspaceNbctlMaxRequests = 30
	nbctlBudget.Lock()
	nbctlBudget.counts = map[string]int{}
	nbctlBudget.expiry = map[string]time.Time{}
	nbctlBudget.Unlock()
	return &calls
}

func nbctlProxyCall(t *testing.T, token, field, account string) *httptest.ResponseRecorder {
	t.Helper()
	doc := nbctlWorkspaceDocuments[field]
	variables := map[string]any{}
	for k, v := range doc.variables {
		variables[k] = v
	}
	if field == "metrics_list" || field == "logs_list" {
		req := map[string]any{}
		for k, v := range doc.variables["request"].(map[string]any) {
			req[k] = v
		}
		req["account_id"] = account
		variables["request"] = req
	} else {
		variables["accountId"] = account
	}
	body, _ := json.Marshal(map[string]any{"query": doc.document, "variables": variables})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, WorkspaceNbctlPath+"/api/graphql", bytes.NewReader(body))
	if token != "" {
		c.Request.Header.Set("Authorization", "Bearer "+token)
	}
	handleWorkspaceNbctlGraphQL(c, nil, nil)
	return w
}

func TestWorkspaceNbctlProxy(t *testing.T) {
	const ws, tenant = "nbctl-workspace", "nbctl-tenant"
	security.SetTenantIdCacheForTest(ws, tenant)
	stubTenantAccounts(t, ws, "nbctl-other")
	admin := func() string {
		tok, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin(tenant, "user-1", []string{ws, "nbctl-other"}), ws, "", time.Minute)
		require.NoError(t, err)
		return tok
	}

	t.Run("an allowed action is forwarded as the app gateway would", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyCall(t, admin(), "metrics_list_names", ws)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"data":{"metrics_list_names":[{"metric":"up"}]}}`, w.Body.String())
		require.Len(t, *calls, 1)
		call := (*calls)[0]
		assert.Equal(t, "/rpc/metrics", call.path)
		assert.Equal(t, "action-token", call.headers.Get("X-ACTION-TOKEN"))
		assert.Equal(t, tenant, call.headers.Get("x-tenant-id"))
		assert.Equal(t, "user-1", call.headers.Get("x-user-id"))
		assert.Equal(t, map[string]any{"name": "metrics_list_names"}, call.body["action"])
		assert.Equal(t, map[string]any{"request": map[string]any{"account_id": ws}}, call.body["input"])
	})

	t.Run("logs go to the logs RPC", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyCall(t, admin(), "logs_list", ws)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "/rpc/logs", (*calls)[0].path)
	})

	t.Run("another account is refused", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyCall(t, admin(), "metrics_list_names", "nbctl-other")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "can only read account "+ws)
		assert.Empty(t, *calls)
	})

	t.Run("an action outside the allowlist is refused", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		body, _ := json.Marshal(map[string]any{"query": `query { tickets_list(request: {account_id: "` + ws + `"}) { id } }`})
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, WorkspaceNbctlPath+"/api/graphql", bytes.NewReader(body))
		c.Request.Header.Set("Authorization", "Bearer "+admin())
		handleWorkspaceNbctlGraphQL(c, nil, nil)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "tickets_list is not available in Nubi's workspace")
		assert.Empty(t, *calls)
	})

	t.Run("a role the app does not allow for the action is refused", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		sc, err := security.NewSecurityContextFromWorkspaceGrants(tenant, security.WorkspaceGrants{UserId: "ns-user", Roles: []string{"k8s_namespace_admin"}, AccountIds: []string{ws}, ScopedEntityIds: map[string][]string{"k8s_namespace_admin": {ws}}})
		require.NoError(t, err)
		tok, err := workspace.ExecutionToken(security.NewRequestContext(t.Context(), sc, nil, nil, nil), ws, "", time.Minute)
		require.NoError(t, err)
		w := nbctlProxyCall(t, tok, "metrics_list", ws)
		assert.Equal(t, http.StatusForbidden, w.Code, "metrics_list excludes k8s_namespace_admin in actions.yaml")
		w = nbctlProxyCall(t, tok, "metrics_list_names", ws)
		assert.Equal(t, http.StatusOK, w.Code, "metrics_list_names includes k8s_namespace_admin: %s", w.Body.String())
		assert.Len(t, *calls, 1)
	})

	t.Run("the per-command budget is enforced", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		config.Config.LlmServerWorkspaceNbctlMaxRequests = 2
		tok := admin()
		for i := 0; i < 2; i++ {
			require.Equal(t, http.StatusOK, nbctlProxyCall(t, tok, "metrics_list_names", ws).Code)
		}
		w := nbctlProxyCall(t, tok, "metrics_list_names", ws)
		assert.Equal(t, http.StatusTooManyRequests, w.Code)
		assert.Len(t, *calls, 2)
		nbctlInFlight.Lock()
		assert.Zero(t, nbctlInFlight.counts[ws], "every path releases its slot, including a budget refusal")
		nbctlInFlight.Unlock()
	})

	t.Run("refused requests spend the budget too", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		config.Config.LlmServerWorkspaceNbctlMaxRequests = 1
		tok := admin()
		assert.Equal(t, http.StatusForbidden, nbctlProxyCall(t, tok, "metrics_list_names", "nbctl-other").Code)
		assert.Equal(t, http.StatusTooManyRequests, nbctlProxyCall(t, tok, "metrics_list_names", ws).Code)
		assert.Empty(t, *calls)
	})

	t.Run("no token, a bad token or the feature off is refused", func(t *testing.T) {
		nbctlProxySetup(t)
		assert.Equal(t, http.StatusUnauthorized, nbctlProxyCall(t, "", "metrics_list_names", ws).Code)
		assert.Equal(t, http.StatusUnauthorized, nbctlProxyCall(t, "not-a-jwt", "metrics_list_names", ws).Code)
		config.Config.LlmServerWorkspaceNbctlEnabled = false
		assert.Equal(t, http.StatusNotFound, nbctlProxyCall(t, admin(), "metrics_list_names", ws).Code)
	})

	t.Run("a bound target is the only account the command can read", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		tok, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin(tenant, "user-1", []string{ws, "nbctl-other"}), ws, "nbctl-other", time.Minute)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, nbctlProxyCall(t, tok, "metrics_list_names", "nbctl-other").Code)
		assert.Equal(t, http.StatusForbidden, nbctlProxyCall(t, tok, "metrics_list_names", ws).Code)
		assert.Len(t, *calls, 1)
	})
}

// Every request nbctl sends fits its action's field allowlist.
func TestNbctlWorkspaceDocumentsFitFieldAllowlist(t *testing.T) {
	for field, doc := range nbctlWorkspaceDocuments {
		t.Run(field, func(t *testing.T) {
			op, err := parseNbctlOperation(doc.document, doc.variables)
			require.NoError(t, err)
			assert.NoError(t, checkNbctlFields("request", op.Args["request"], workspaceNbctlActions[field].fields))
		})
	}
}

// nbctl's list-label-values without --index sends this document instead.
// A comment holding a bracket inside the variables or the selection set does
// not end the group early.
func TestParseNbctlOperationSkipsComments(t *testing.T) {
	op, err := parseNbctlOperation(`query Q($a: String! # ) not the end
) {
  metrics_list_names(request: {account_id: $a}) {
    metric # } not the end
  }
}`, map[string]any{"a": "acct"})
	require.NoError(t, err)
	assert.Equal(t, "metrics_list_names", op.Field)
	assert.Equal(t, map[string]any{"account_id": "acct"}, op.Args["request"])
}

// A non-ASCII character in a string or a comment is not split as whitespace.
func TestParseNbctlOperationKeepsUTF8(t *testing.T) {
	op, err := parseNbctlOperation("query Q($a: String!) { # caf\u00e9\u00a0\n  logs_list(request: {account_id: $a, query: \"{app=\\\"caf\u00e9\u00a0\\\"}\"}) { logs { message } }\n}", map[string]any{"a": "acct"})
	require.NoError(t, err)
	assert.Equal(t, "{app=\"caf\u00e9\u00a0\"}", op.Args["request"].(map[string]any)["query"])
}

func TestNbctlDefaultLabelValuesDocument(t *testing.T) {
	op, err := parseNbctlOperation(`query FetchLogLabelValues($accountId: String!, $labelName: String!, $query: String!) {
  logs_list_label_values(request: {account_id: $accountId, label_name: $labelName, request: {query: $query}}) {
    value
  }
}`, map[string]any{"accountId": "acct", "labelName": "namespace", "query": "start=1&end=2"})
	require.NoError(t, err)
	assert.NoError(t, checkNbctlFields("request", op.Args["request"], workspaceNbctlActions["logs_list_label_values"].fields))
}

func nbctlProxyRaw(t *testing.T, token, document string, variables map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": document, "variables": variables})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, WorkspaceNbctlPath+"/api/graphql", bytes.NewReader(body))
	c.Request.Header.Set("Authorization", "Bearer "+token)
	handleWorkspaceNbctlGraphQL(c, nil, nil)
	return w
}

func TestNbctlProxyForwardsOnlyNbctlFields(t *testing.T) {
	const ws, tenant = "nbctl-fields-workspace", "nbctl-fields-tenant"
	security.SetTenantIdCacheForTest(ws, tenant)
	stubTenantAccounts(t, ws)
	token := func() string {
		tok, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin(tenant, "user-1", []string{ws}), ws, "", time.Minute)
		require.NoError(t, err)
		return tok
	}

	refused := map[string]string{
		"request not object": `query { metrics_list_names(request: "` + ws + `") { metric } }`,
		// Backend options nbctl never sets: a custom endpoint with a stored
		// secret filled in would send that secret wherever the request says.
		"integration settings":          `query { logs_list_labels(request: {account_id: "` + ws + `", log_provider: "elasticsearch", integration_id: "i-1", integration_config_values: [{name: "url", value: "https://example.com"}], request: {query: ""}}) { label } }`,
		"provider params not an object": `query { logs_list_labels(request: {account_id: "` + ws + `", request: "x"}) { label } }`,
		"metrics query option":          `query { metrics_list(request: {account_id: "` + ws + `", queries: {query: "up", datasource: "x"}}) { results } }`,
		"extra argument":                `query { metrics_list_names(request: {account_id: "` + ws + `"}, tenant_id: "other") { metric } }`,
	}
	for name, doc := range refused {
		t.Run(name, func(t *testing.T) {
			calls := nbctlProxySetup(t)
			w := nbctlProxyRaw(t, token(), doc, nil)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Empty(t, *calls)
		})
	}

	t.Run("provider parameters pass through to the provider", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyRaw(t, token(), nbctlWorkspaceDocuments["logs_list"].document, map[string]any{
			"request": map[string]any{"account_id": ws, "query": `{"query":{"match_all":{}}}`, "request": map[string]any{"index": "logs-*", "query_type": "dsl"}},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Len(t, *calls, 1)
		forwarded := (*calls)[0].body["input"].(map[string]any)["request"].(map[string]any)
		assert.Equal(t, map[string]any{"index": "logs-*", "query_type": "dsl"}, forwarded["request"])
	})

	t.Run("a JSON object query gets query_type dsl unless the request names one", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyRaw(t, token(), nbctlWorkspaceDocuments["metrics_list"].document, map[string]any{
			"request": map[string]any{"account_id": ws, "queries": map[string]any{"query": `{"query":{"match_all":{}}}`}},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		w = nbctlProxyRaw(t, token(), nbctlWorkspaceDocuments["logs_list"].document, map[string]any{
			"request": map[string]any{"account_id": ws, "query": "service:x", "request": map[string]any{"query_type": "kql"}},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Len(t, *calls, 2)
		params := func(i int) any {
			return (*calls)[i].body["input"].(map[string]any)["request"].(map[string]any)["request"]
		}
		assert.Equal(t, map[string]any{"query_type": "dsl"}, params(0))
		assert.Equal(t, map[string]any{"query_type": "kql"}, params(1), "an explicit query_type is kept")
		w = nbctlProxyRaw(t, token(), nbctlWorkspaceDocuments["logs_list"].document, map[string]any{
			"request": map[string]any{"account_id": ws, "query": `{app="x"}`},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Nil(t, params(2), "a LogQL query gets no provider parameters")
	})

	t.Run("only the request argument is forwarded and limit is capped", func(t *testing.T) {
		calls := nbctlProxySetup(t)
		w := nbctlProxyRaw(t, token(), nbctlWorkspaceDocuments["logs_list"].document, map[string]any{
			"request": map[string]any{"account_id": ws, "query": `{app="x"}`, "limit": 100000.0},
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		require.Len(t, *calls, 1)
		assert.Equal(t, map[string]any{"request": map[string]any{"account_id": ws, "query": `{app="x"}`, "limit": float64(nbctlMaxLogLimit)}}, (*calls)[0].body["input"])
	})
}

func TestNbctlProxyBackendErrors(t *testing.T) {
	const ws, tenant = "nbctl-backend-workspace", "nbctl-backend-tenant"
	security.SetTenantIdCacheForTest(ws, tenant)
	stubTenantAccounts(t, ws)
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"an error keeps its reason but not addresses": {http.StatusInternalServerError, `bad query: dial tcp 10.1.2.3:9200 refused (see http://prom.internal:9090/api)`, "bad query: dial tcp <address> refused (see <address>)"},
		"a body that is not JSON":                     {http.StatusOK, ``, "returned an unreadable response"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			nbctlProxySetup(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			config.Config.ServiceEndpoint = srv.URL
			tok, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin(tenant, "user-1", []string{ws}), ws, "", time.Minute)
			require.NoError(t, err)
			w := nbctlProxyCall(t, tok, "metrics_list_names", ws)
			var resp struct {
				Errors []struct{ Message string } `json:"errors"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.Len(t, resp.Errors, 1, w.Body.String())
			assert.Contains(t, resp.Errors[0].Message, tc.want)
			assert.NotContains(t, w.Body.String(), "10.1.2.3")
			assert.NotContains(t, w.Body.String(), "prom.internal")
		})
	}
}

// An expired token's budget resets, and other expired tokens are swept.
func TestNbctlSpendBudgetExpiry(t *testing.T) {
	nbctlBudget.Lock()
	nbctlBudget.counts, nbctlBudget.expiry, nbctlBudget.lastSweep = map[string]int{}, map[string]time.Time{}, time.Time{}
	nbctlBudget.Unlock()
	start := time.Unix(1_800_000_000, 0)
	require.True(t, nbctlSpendBudget("a", 1, start))
	require.True(t, nbctlSpendBudget("b", 1, start))
	assert.False(t, nbctlSpendBudget("a", 1, start.Add(time.Minute)), "budget used up within its window")
	later := start.Add(11 * time.Minute)
	assert.True(t, nbctlSpendBudget("a", 1, later), "an expired token starts a new window")
	nbctlBudget.Lock()
	defer nbctlBudget.Unlock()
	assert.NotContains(t, nbctlBudget.counts, func() string {
		sum := sha256.Sum256([]byte("b"))
		return hex.EncodeToString(sum[:])
	}(), "the sweep removed the other expired token")
}

func TestNbctlRedactResultMessages(t *testing.T) {
	// A provider error object and an escaped quote after a URL stay valid JSON.
	withObject := []byte(`{"suggestion":"see http://es.internal:9200/x\"y\"","results":[{"query_key":"q","error":{"reason":"connect to http://10.1.2.3:9200 failed"}}]}`)
	redacted := nbctlRedactResultMessages(withObject)
	require.True(t, json.Valid(redacted), string(redacted))
	assert.NotContains(t, string(redacted), "10.1.2.3")
	assert.NotContains(t, string(redacted), "es.internal")

	body := []byte(`{"results":[{"query_key":"q","error":"dial tcp 10.1.2.3:9200: lookup es-master.obs.svc.cluster.local: no such host","payload":[{"value":12345678901234}]}],"logs":[{"message":"GET from 10.0.0.9"}]}`)
	got := string(nbctlRedactResultMessages(body))
	assert.NotContains(t, got, "10.1.2.3")
	assert.NotContains(t, got, "es-master.obs.svc.cluster.local")
	assert.Contains(t, got, "GET from 10.0.0.9", "provider data is returned as is")
	assert.Contains(t, got, "12345678901234", "numbers keep their exact value")
	clean := []byte(`{"results":[{"query_key":"q","payload":[]}]}`)
	assert.Equal(t, string(clean), string(nbctlRedactResultMessages(clean)), "a body with nothing to redact is untouched")
}

// The env nbctl gets and the route the proxy serves name the same path.
func TestNbctlProxyRouteMatchesWorkspaceEnv(t *testing.T) {
	assert.Equal(t, "/api/v1/workspace/nbctl", WorkspaceNbctlPath, "workspace/execution_token.go sets NUDGEBEE_ENDPOINT to this path")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handleWorkspaceApis(r, nil, nil)
	found := false
	for _, route := range r.Routes() {
		if route.Method == http.MethodPost && route.Path == WorkspaceNbctlPath+"/api/graphql" {
			found = true
		}
	}
	assert.True(t, found, "nbctl posts to NUDGEBEE_ENDPOINT + /api/graphql")
}

func TestNbctlProxyErrorsHideInternalAddresses(t *testing.T) {
	const ws, tenant = "nbctl-err-workspace", "nbctl-err-tenant"
	security.SetTenantIdCacheForTest(ws, tenant)
	stubTenantAccounts(t, ws)
	nbctlProxySetup(t)
	config.Config.ServiceEndpoint = "http://127.0.0.1:1" // nothing listens here
	tok, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin(tenant, "user-1", []string{ws}), ws, "", time.Minute)
	require.NoError(t, err)
	w := nbctlProxyCall(t, tok, "metrics_list_names", ws)
	assert.Contains(t, w.Body.String(), "the metrics and logs service is unavailable right now")
	assert.NotContains(t, w.Body.String(), "127.0.0.1")
	assert.NotContains(t, w.Body.String(), "/rpc/")
}

func TestNbctlCheckProviderRequest(t *testing.T) {
	// The checks do not depend on the account's provider, which a failed
	// lookup can get wrong.
	refused := map[string]map[string]any{
		"index reaching another endpoint": {"query": "{}", "request": map[string]any{"index": "logs/_update_by_query?conflicts=proceed#"}},
		"metric index with a path":        {"queries": map[string]any{"query": "{}"}, "request": map[string]any{"metric_name": "m/_doc#"}},
		"index with a space":              {"query": "x", "request": map[string]any{"index": "a b"}},
		"script in a query":               {"query": `{"query":{"script":{"script":"1"}}}`},
		"global aggregation":              {"query": `{"size":0,"aggs":{"g":{"global":{},"aggs":{"t":{"top_hits":{}}}}}}`},
		"runtime mappings in metrics":     {"queries": map[string]any{"query": `{"runtime_mappings":{}}`}},
		"ppl names its own index":         {"query": "source=other", "request": map[string]any{"query_type": "ppl"}},
		"wrapper hides the real query":    {"query": `{"query":{"wrapper":{"query":"eyJzY3JpcHQiOnt9fQ=="}}}`},
		"size that is not a number":       {"query": `{"size":"lots","query":{"match_all":{}}}`},
		"size of the wrong type":          {"query": `{"size":true,"query":{"match_all":{}}}`},
	}
	for name, request := range refused {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, nbctlCheckProviderRequest(request))
		})
	}

	ok := map[string]any{"query": `{"size":100000,"query":{"match_all":{}},"aggs":{"p":{"terms":{"field":"kubernetes.pod_name"}}}}`, "request": map[string]any{"index": "logs-kubernetes.container_logs-*", "query_type": "kql"}}
	require.NoError(t, nbctlCheckProviderRequest(ok))
	assert.Contains(t, ok["query"], `"size":5000`, "an Elasticsearch size above the logs limit is clamped")
	// Loki and Prometheus queries are not JSON bodies; only index-like params are checked.
	assert.NoError(t, nbctlCheckProviderRequest(map[string]any{"query": `sum by (pod) (count_over_time({namespace="x"} |= "script" [1h]))`}))

	asString := map[string]any{"query": `{"size":"100000","query":{"match_all":{}}}`}
	require.NoError(t, nbctlCheckProviderRequest(asString))
	assert.Contains(t, asString["query"], `"size":5000`, "a numeric-string size is clamped too")

	// A non-string query beside queries.query must not take the clamp.
	mixed := map[string]any{"query": 7.0, "queries": map[string]any{"query": `{"size":20000}`}}
	require.NoError(t, nbctlCheckProviderRequest(mixed))
	assert.Equal(t, 7.0, mixed["query"])
	assert.Contains(t, mixed["queries"].(map[string]any)["query"], `"size":5000`)

	metrics := map[string]any{"queries": map[string]any{"query": `{"size":20000,"query":{"match_all":{}}}`}}
	require.NoError(t, nbctlCheckProviderRequest(metrics))
	assert.Contains(t, metrics["queries"].(map[string]any)["query"], `"size":5000`, "the clamp writes back into queries.query")
}

func TestNbctlConcurrencyPerAccount(t *testing.T) {
	var releases []func()
	for i := 0; i < nbctlMaxConcurrentPerAccount; i++ {
		release, ok := nbctlAcquireSlot("acct-busy")
		require.True(t, ok)
		releases = append(releases, release)
	}
	_, ok := nbctlAcquireSlot("acct-busy")
	assert.False(t, ok, "the account is at its limit")
	_, ok = nbctlAcquireSlot("acct-other")
	assert.True(t, ok, "other accounts are unaffected")
	releases[0]()
	releases[0]() // a second call must not free another slot
	_, ok = nbctlAcquireSlot("acct-busy")
	assert.True(t, ok, "a released slot is reusable")
	_, ok = nbctlAcquireSlot("acct-busy")
	assert.False(t, ok, "release is idempotent")
}
