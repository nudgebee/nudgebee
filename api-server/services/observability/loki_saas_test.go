package observability

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"nudgebee/services/relay"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLokiSaasCtx() *security.RequestContext {
	return security.NewRequestContext(context.Background(), security.NewSecurityContextForSuperAdmin(), slog.Default(), nil, nil)
}

// patchLokiIntegration stubs the integration lookup GetLokiConfig makes, and — the point
// of the exercise — fails the test if anything reaches the relay. The user-source path
// must never call the agent, and an accidental relay call is otherwise invisible: it
// would just fail at runtime on accounts that have no agent.
func patchLokiIntegration(t *testing.T, dtos []core.IntegrationDto) {
	t.Helper()
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(core.ListIntegrationConfigs,
		func(_ *security.RequestContext, _ string, _ string) ([]core.IntegrationDto, error) {
			return dtos, nil
		})
	patches.ApplyFunc(relay.Execute, func(_ relay.RelayExecuteRequest) (map[string]any, error) {
		t.Fatal("LokiSaasSource must not use the agent relay")
		return nil, nil
	})
	t.Cleanup(patches.Reset)
}

// lokiIntegrationDto builds a user-source loki integration row from plain config values.
func lokiIntegrationDto(values map[string]string) []core.IntegrationDto {
	configs := make([]core.IntegrationConfigValue, 0, len(values))
	for name, value := range values {
		configs = append(configs, core.IntegrationConfigValue{Name: name, Value: value})
	}
	return []core.IntegrationDto{{Id: "int-loki", Type: "loki", Source: "user", Configs: configs}}
}

func TestLokiSaasQueryLogs_BuildsRangeRequest(t *testing.T) {
	var gotPath, gotQuery string
	var gotAuthOK bool
	var gotUser, gotPass, gotTenant, gotCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotUser, gotPass, gotAuthOK = r.BasicAuth()
		gotTenant = r.Header.Get("X-Scope-OrgID")
		gotCustom = r.Header.Get("X-Extra")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"namespace":"demo","pod":"demo-abc"},"values":[["1700000000000000000","boom failed"]]}
		]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
		"loki_url":       server.URL,
		"loki_auth_type": LokiAuthBasic,
		"loki_username":  "tenant-1",
		"loki_password":  "s3cret",
		"loki_tenant_id": "org-7",
		"loki_headers":   `{"X-Extra":"yes"}`,
	}))

	source := &LokiSaasSource{}
	logs, err := source.QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
		Limit:     100,
	})
	require.NoError(t, err)

	assert.Equal(t, "/loki/api/v1/query_range", gotPath)
	params, parseErr := url.ParseQuery(gotQuery)
	require.NoError(t, parseErr)
	assert.Equal(t, `{namespace="demo"}`, params.Get("query"))
	assert.Equal(t, "100", params.Get("limit"))
	assert.Equal(t, "backward", params.Get("direction"))
	// StartTime/EndTime are epoch milliseconds; Loki takes nanoseconds.
	assert.Equal(t, "1700000000000000000", params.Get("start"))
	assert.Equal(t, "1700000600000000000", params.Get("end"))

	assert.True(t, gotAuthOK, "basic auth must be sent when both credentials are set")
	assert.Equal(t, "tenant-1", gotUser)
	assert.Equal(t, "s3cret", gotPass)
	assert.Equal(t, "org-7", gotTenant)
	assert.Equal(t, "yes", gotCustom)

	require.Len(t, logs, 1)
	assert.Equal(t, "boom failed", logs[0].Message)
	assert.Equal(t, "demo", logs[0].Labels["namespace"])
}

// A username with no password is not a credential. Sending it would turn an
// anonymous-read Loki into a 401.
func TestLokiSaasQueryLogs_NoBasicAuthWithoutBothCredentials(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, sawAuth = r.BasicAuth()
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
		"loki_url":       server.URL,
		"loki_auth_type": LokiAuthBasic,
		"loki_username":  "only-user",
	}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.False(t, sawAuth, "a username without a password must not be sent as basic auth")
}

func TestLokiSaasQueryLogs_BearerAuth(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
		"loki_url":          server.URL,
		"loki_auth_type":    LokiAuthBearer,
		"loki_bearer_token": "tok-123",
	}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok-123", gotAuth)
}

// Switching auth method must not leave the previous method's credential in play: the
// stored value survives the switch, and sending it would authenticate as the wrong
// identity or turn an anonymous-read Loki into a 401.
func TestLokiSaasQueryLogs_SendsOnlyTheSelectedMethod(t *testing.T) {
	var gotAuth string
	var sawBasic bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _, sawBasic = r.BasicAuth()
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
		"loki_url":          server.URL,
		"loki_auth_type":    LokiAuthNone,
		"loki_username":     "leftover",
		"loki_password":     "leftover",
		"loki_bearer_token": "leftover",
	}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.False(t, sawBasic, "auth_type=none must not send leftover basic credentials")
	assert.Empty(t, gotAuth, "auth_type=none must not send a leftover bearer token")
}

func TestLokiSaasQueryLogs_BuilderModeQuery(t *testing.T) {
	var gotLogQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLogQL = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: map[string]map[query.BinaryWhereClauseType]interface{}{
					"namespace": {query.Eq: "demo"},
				},
			},
		},
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.Contains(t, gotLogQL, `namespace="demo"`)
}

// The provider's own error text is the whole diagnosis for a rejected LogQL query;
// dropping it is what makes a bad query look like "no logs found".
func TestLokiSaasQueryLogs_PropagatesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("parse error at line 1: syntax error"))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace=}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.Contains(t, err.Error(), "syntax error")
}

// Loki answers a valid query over a window with no data (e.g. past retention) with
// success and a null data object. That is an empty result, not a failure.
func TestLokiSaasQueryLogs_NullDataIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":null}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	logs, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.Empty(t, logs)
}

func TestLokiSaasQueryLogs_RejectsOversizeLimit(t *testing.T) {
	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": "http://loki.invalid"}))

	_, err := (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1",
		Query:     `{namespace="demo"}`,
		StartTime: 1700000000000,
		EndTime:   1700000600000,
		Limit:     5001,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit exceeds maximum of 5000")
}

func TestLokiSaasQueryLabels(t *testing.T) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success","data":["namespace","pod"]}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	labels, err := (&LokiSaasSource{}).QueryLabels(newLokiSaasCtx(), FetchLogLabelRequest{
		AccountId: "acc-1",
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)

	assert.Equal(t, "/loki/api/v1/labels", gotPath)
	params, parseErr := url.ParseQuery(gotQuery)
	require.NoError(t, parseErr)
	assert.Equal(t, "1700000000000000000", params.Get("start"))
	assert.Equal(t, "1700000600000000000", params.Get("end"))

	require.Len(t, labels, 2)
	assert.Equal(t, "namespace", labels[0].Label)
}

// The frontend sends Loki-native "start=<ns>&end=<ns>" strings for label lookups —
// shaped for the relay, but valid API parameters on the direct path too.
func TestLokiSaasQueryLabels_PassesThroughRequestQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"success","data":["namespace"]}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	_, err := (&LokiSaasSource{}).QueryLabels(newLokiSaasCtx(), FetchLogLabelRequest{
		AccountId: "acc-1",
		Request:   map[string]any{"query": "start=111000000&end=222000000"},
	})
	require.NoError(t, err)

	params, parseErr := url.ParseQuery(gotQuery)
	require.NoError(t, parseErr)
	assert.Equal(t, "111000000", params.Get("start"))
	assert.Equal(t, "222000000", params.Get("end"))
}

func TestLokiSaasQueryLabels_NullDataIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	labels, err := (&LokiSaasSource{}).QueryLabels(newLokiSaasCtx(), FetchLogLabelRequest{
		AccountId: "acc-1",
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)
	assert.Empty(t, labels)
}

func TestLokiSaasQueryLabelValues_EscapesLabelName(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"status":"success","data":["demo","other"]}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	values, err := (&LokiSaasSource{}).QueryLabelValues(newLokiSaasCtx(), FetchLogLabelValuesRequest{
		AccountId: "acc-1",
		LabelName: "app/name",
		StartTime: 1700000000000,
		EndTime:   1700000600000,
	})
	require.NoError(t, err)

	assert.Equal(t, "/loki/api/v1/label/app%2Fname/values", gotPath)
	require.Len(t, values, 2)
	assert.Equal(t, "demo", values[0].Value)
}

func TestLokiSaasQueryLogGroup_GroupsWithoutRelay(t *testing.T) {
	var gotLogQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLogQL = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"namespace":"demo","pod":"demo-abc"},"values":[
				["1700000000000000000","ERROR connection refused to 10.0.0.1"],
				["1700000001000000000","ERROR connection refused to 10.0.0.2"]
			]}
		]}}`))
	}))
	defer server.Close()

	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": server.URL}))

	out, err := (&LokiSaasSource{}).QueryLogGroup(newLokiSaasCtx(), FetchLogGroupRequest{
		AccountId: "acc-1",
		StartTime: 1700000000000,
		EndTime:   1700000600000,
		Request:   map[string]any{"selectedNamespace": "demo"},
	})
	require.NoError(t, err)

	assert.Contains(t, gotLogQL, `namespace="demo"`)
	assert.NotEmpty(t, out.Groups)
}

// The two Loki sources must agree on everything transport-independent, or the panel and
// the query path can describe different mappings.
func TestLokiSaasDelegatesSharedBehaviour(t *testing.T) {
	saas := &LokiSaasSource{}
	agent := &LokiSource{}
	assert.Equal(t, agent.GetLabelMapping(), saas.GetLabelMapping())
	assert.Equal(t, agent.GetSupportedOperators(), saas.GetSupportedOperators())
	assert.Equal(t, providerRef{Provider: "loki", Source: "user"}, saas.ProviderRef())
}

func TestGetLokiConfig(t *testing.T) {
	t.Run("ignores agent-source rows", func(t *testing.T) {
		patchLokiIntegration(t, []core.IntegrationDto{
			{Id: "int-agent", Type: "loki", Source: "agent", Configs: []core.IntegrationConfigValue{
				{Name: "default_log_provider", Value: "true"},
			}},
		})
		_, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no loki integration configured")
	})

	t.Run("requires a url", func(t *testing.T) {
		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_username": "u"}))
		_, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing loki_url")
	})

	t.Run("trims trailing slashes and parses headers", func(t *testing.T) {
		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
			"loki_url":     "http://loki.example.com/",
			"loki_headers": `{"Authorization":"Bearer abc"}`,
		}))
		cfg, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "http://loki.example.com", cfg.Url)
		assert.Equal(t, "Bearer abc", cfg.Headers["Authorization"])
	})

	t.Run("rejects malformed headers", func(t *testing.T) {
		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
			"loki_url":     "http://loki.example.com",
			"loki_headers": `not-json`,
		}))
		_, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "loki_headers")
	})

	t.Run("ruler url falls back to the query url", func(t *testing.T) {
		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": "http://loki.example.com"}))
		cfg, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "http://loki.example.com", cfg.RulerBaseURL())

		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
			"loki_url":       "http://loki.example.com",
			"loki_rules_url": "http://ruler.example.com",
		}))
		cfg, err = GetLokiConfig(newLokiSaasCtx(), "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "http://ruler.example.com", cfg.RulerBaseURL())
	})
}

// The integration form validates loki_headers at save time and GetLokiConfig parses it
// at query time. They must agree, or a config saves as valid and then breaks every
// query — which is why both call one shared parser.
func TestGetLokiConfig_HeaderParsingMatchesValidation(t *testing.T) {
	for _, raw := range []string{`{"A":null}`, `{"X-Num":5}`, `[1,2]`, `"str"`} {
		_, validateErr := core.ParseExtraHeaders(raw)
		patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
			"loki_url":     "http://loki.example.com",
			"loki_headers": raw,
		}))
		_, runtimeErr := GetLokiConfig(newLokiSaasCtx(), "acc-1")
		assert.Equal(t, validateErr != nil, runtimeErr != nil,
			"save-time and query-time header parsing must agree for %s", raw)
	}
}

// getLogSource is the only gate deciding whether a provider serves logs at all, so the
// user pair has to resolve to the direct source and not the relay one.
func TestGetLogSource_LokiUser(t *testing.T) {
	source, err := getLogSource("loki", "user")
	require.NoError(t, err)
	assert.IsType(t, &LokiSaasSource{}, source)

	agentSource, err := getLogSource("loki", "agent")
	require.NoError(t, err)
	assert.IsType(t, &LokiSource{}, agentSource)
}

// A guard on the envelope shape: convertLokiResponse takes the inner data object, so
// handing it the whole body would silently parse to zero results.
func TestLokiSaasQueryLogs_UnwrapsEnvelopeBeforeParsing(t *testing.T) {
	body := `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"pod":"p"},"values":[["1700000000000000000","hello"]]}
	]}}`
	var envelope lokiEnvelope
	require.NoError(t, json.Unmarshal([]byte(body), &envelope))
	assert.True(t, strings.HasPrefix(string(envelope.Data), `{"resultType"`))

	logs, err := (&LokiSource{}).convertLokiResponse(envelope.Data)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "hello", logs[0].Message)
}

// A row saved before the auth selector existed has no loki_auth_type. Its credentials
// must keep working exactly as they did, or upgrading breaks every configured Loki.
func TestGetLokiConfig_LegacyRowKeepsAuthenticating(t *testing.T) {
	var gotUser, gotPass string
	var gotAuthOK bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotAuthOK = r.BasicAuth()
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	defer server.Close()

	// No loki_auth_type key at all — exactly what a pre-upgrade row looks like.
	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{
		"loki_url":      server.URL,
		"loki_username": "legacy-user",
		"loki_password": "legacy-pass",
	}))

	cfg, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
	require.NoError(t, err)
	assert.Equal(t, LokiAuthBasic, cfg.AuthType, "legacy basic credentials must infer basic auth")

	_, err = (&LokiSaasSource{}).QueryLogs(newLokiSaasCtx(), FetchLogRequest{
		AccountId: "acc-1", Query: `{namespace="demo"}`,
		StartTime: 1700000000000, EndTime: 1700000600000,
	})
	require.NoError(t, err)
	assert.True(t, gotAuthOK, "legacy row must still send basic auth")
	assert.Equal(t, "legacy-user", gotUser)
	assert.Equal(t, "legacy-pass", gotPass)
}

// A legacy row with no credentials at all stays anonymous.
func TestGetLokiConfig_LegacyAnonymousRow(t *testing.T) {
	patchLokiIntegration(t, lokiIntegrationDto(map[string]string{"loki_url": "http://loki.invalid"}))
	cfg, err := GetLokiConfig(newLokiSaasCtx(), "acc-1")
	require.NoError(t, err)
	assert.Equal(t, LokiAuthNone, cfg.AuthType)
}
