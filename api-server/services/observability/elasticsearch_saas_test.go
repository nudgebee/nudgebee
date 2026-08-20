package observability

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/services/integrations/core"
	"nudgebee/services/query"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Coverage note for elasticsearch_saas.go
//
// Every pure / HTTP-injectable function in the file is exercised below.
// The remaining exported methods are thin orchestration over these helpers +
// GetElasticsearchConfig (which reads a live, encrypted integration record from
// the DB) and cannot run as a unit test without integration infrastructure:
//
//   - GetElasticsearchConfig  -> DB read + Decrypt; its validation logic is
//                                extracted into validateESConfig (tested here).
//   - QueryLogs               -> body building (buildESKQLQueryBody [see
//                                elasticsearch_kql_test.go] / finalizeESLogQueryBody)
//                                + parseESSearchLogs / parseESPPLLogs, tested here.
//   - QueryLabels             -> ListAllESIndexTargets + a trivial name->label map.
//   - QueryLabelValues        -> parseESLabelValuesResponse (tested here).
//   - QueryIndexFields        -> parseESMappingFields (tested here).
//   - QueryLogGroup           -> QueryLogs + groupESLogsByPattern (tested in the
//                                elasticsearch grouping suite).
//   - getCognitoAWSCredentials-> live AWS Cognito; external dependency, no unit.
//   - the esRequest "cognito"  branch -> SigV4 needs live AWS creds; the basic /
//                                api_key / bearer_token / TLS branches are tested.
// ---------------------------------------------------------------------------

// ---------- buildElasticsearchConfigFromValues ----------

func TestBuildElasticsearchConfigFromValues_AllAuthAndTLS(t *testing.T) {
	cfg := BuildElasticsearchConfigFromValues([]core.IntegrationConfigValue{
		{Name: ElasticsearchUrl, Value: "  https://es.example.com/  "},
		{Name: ElasticsearchAuthType, Value: "api_key"},
		{Name: ElasticsearchApiKey, Value: "abc=="},
		{Name: ElasticsearchBearerToken, Value: "tok"},
		{Name: ElasticsearchUsername, Value: "u"},
		{Name: ElasticsearchPassword, Value: "p"},
		{Name: ElasticsearchRegion, Value: "us-east-1"},
		{Name: ElasticsearchUserPoolId, Value: "pool"},
		{Name: ElasticsearchIdentityPoolId, Value: "idpool"},
		{Name: ElasticsearchAppClientId, Value: "client"},
		{Name: ElasticsearchTLSSkipVerify, Value: "TRUE"},
	})
	assert.Equal(t, "https://es.example.com", cfg.Url) // trimmed + trailing slash removed
	assert.Equal(t, "api_key", cfg.AuthType)
	assert.Equal(t, "abc==", cfg.ApiKey)
	assert.Equal(t, "tok", cfg.BearerToken)
	assert.Equal(t, "u", cfg.Username)
	assert.Equal(t, "p", cfg.Password)
	assert.Equal(t, "us-east-1", cfg.Region)
	assert.Equal(t, "pool", cfg.UserPoolId)
	assert.Equal(t, "idpool", cfg.IdentityPoolId)
	assert.Equal(t, "client", cfg.AppClientId)
	assert.True(t, cfg.TLSSkipVerify) // case-insensitive "TRUE"
}

func TestBuildElasticsearchConfigFromValues_DefaultsAndTLSFalse(t *testing.T) {
	cfg := BuildElasticsearchConfigFromValues([]core.IntegrationConfigValue{
		{Name: ElasticsearchUrl, Value: "https://es"},
		{Name: ElasticsearchTLSSkipVerify, Value: "no"},
	})
	assert.Equal(t, "basic", cfg.AuthType) // empty auth_type defaults to basic
	assert.False(t, cfg.TLSSkipVerify)     // anything other than "true" is false
}

// ---------- validateESConfig ----------

func TestValidateESConfig(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *ElasticsearchConfig
		wantErr string
	}{
		{"missing url", &ElasticsearchConfig{AuthType: "basic", Username: "u", Password: "p"}, "URL"},
		{"basic ok", &ElasticsearchConfig{Url: "x", AuthType: "basic", Username: "u", Password: "p"}, ""},
		{"basic missing creds", &ElasticsearchConfig{Url: "x", AuthType: "basic", Username: "u"}, "username/password"},
		{"cognito uses basic creds", &ElasticsearchConfig{Url: "x", AuthType: "cognito", Username: "u", Password: "p"}, ""},
		{"cognito missing creds", &ElasticsearchConfig{Url: "x", AuthType: "cognito"}, "username/password"},
		{"api_key ok", &ElasticsearchConfig{Url: "x", AuthType: "api_key", ApiKey: "k"}, ""},
		{"api_key missing", &ElasticsearchConfig{Url: "x", AuthType: "api_key"}, "api_key"},
		{"bearer ok", &ElasticsearchConfig{Url: "x", AuthType: "bearer_token", BearerToken: "t"}, ""},
		{"bearer missing", &ElasticsearchConfig{Url: "x", AuthType: "bearer_token"}, "bearer_token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateESConfig(tc.cfg)
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// ---------- basicAuthHeader / hashPayload ----------

func TestBasicAuthHeader(t *testing.T) {
	// base64("user:pass") == dXNlcjpwYXNz
	assert.Equal(t, "Basic dXNlcjpwYXNz", basicAuthHeader("user", "pass"))
}

func TestHashPayload(t *testing.T) {
	// Known SHA-256 vectors.
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hashPayload(""))
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", hashPayload("hello"))
}

// ---------- esRequest / esRequestJSON ----------

func TestESRequest_AuthHeadersAndBody(t *testing.T) {
	var gotAuth, gotBody, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	t.Run("basic", func(t *testing.T) {
		cfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "basic", Username: "user", Password: "pass"}
		resp, err := esRequest("POST", srv.URL, `{"q":1}`, cfg)
		assert.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, basicAuthHeader("user", "pass"), gotAuth)
		assert.Equal(t, "application/json", gotContentType)
		assert.Equal(t, `{"q":1}`, gotBody) // body passed through verbatim
	})

	t.Run("api_key", func(t *testing.T) {
		cfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "api_key", ApiKey: "KEY123"}
		resp, err := esRequest("GET", srv.URL, "", cfg)
		assert.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, "ApiKey KEY123", gotAuth)
	})

	t.Run("bearer_token", func(t *testing.T) {
		cfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "bearer_token", BearerToken: "TOK"}
		resp, err := esRequest("GET", srv.URL, "", cfg)
		assert.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, "Bearer TOK", gotAuth)
	})
}

func TestESRequest_NewRequestError(t *testing.T) {
	// An invalid HTTP method makes http.NewRequest fail before any dial.
	_, err := esRequest("BAD METHOD", "http://example.com", "", &ElasticsearchConfig{AuthType: "basic"}) //nolint:bodyclose // error path: resp is nil
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create request")
}

func TestESRequest_TLSSkipVerifySelectsClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Verified client rejects the self-signed cert.
	verifyCfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "basic", Username: "u", Password: "p"}
	_, err := esRequest("GET", srv.URL, "", verifyCfg) //nolint:bodyclose // error path: resp is nil on TLS failure
	assert.Error(t, err, "expected TLS verification to reject the self-signed cert")

	// Opt-in skip client accepts it.
	skipCfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "basic", Username: "u", Password: "p", TLSSkipVerify: true}
	resp, err := esRequest("GET", srv.URL, "", skipCfg)
	assert.NoError(t, err)
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestESRequestJSON_MarshalsBodyAndSucceeds(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &ElasticsearchConfig{Url: srv.URL, AuthType: "basic", Username: "u", Password: "p"}
	resp, err := esRequestJSON("POST", srv.URL, map[string]any{"size": 5}, cfg)
	assert.NoError(t, err)
	_ = resp.Body.Close()
	assert.JSONEq(t, `{"size":5}`, gotBody)
}

func TestESRequestJSON_MarshalError(t *testing.T) {
	// A channel is not JSON-serializable -> marshal fails before any request.
	_, err := esRequestJSON("POST", "http://x", make(chan int), &ElasticsearchConfig{AuthType: "basic"}) //nolint:bodyclose // error path: resp is nil on marshal failure
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal request body")
}

// ---------- readResponse ----------

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (errReadCloser) Close() error             { return nil }

func TestReadResponse(t *testing.T) {
	t.Run("200 returns body", func(t *testing.T) {
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("payload"))}
		got, err := readResponse(resp, "op")
		assert.NoError(t, err)
		assert.Equal(t, "payload", string(got))
	})
	t.Run("non-200 is an error carrying status and body", func(t *testing.T) {
		resp := &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("kaboom"))}
		_, err := readResponse(resp, "es query")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "status 500")
		assert.Contains(t, err.Error(), "kaboom")
		assert.Contains(t, err.Error(), "es query")
	})
	t.Run("body read failure", func(t *testing.T) {
		resp := &http.Response{StatusCode: 200, Body: errReadCloser{}}
		_, err := readResponse(resp, "es query")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to read es query response body")
	})
}

// ---------- GetLabelMapping / GetSupportedOperators / GetQuery ----------

func TestElasticSaasSource_LabelMappingAndOperators(t *testing.T) {
	var e ElasticSaasSource
	assert.Equal(t, map[string]string{}, e.GetLabelMapping())
	assert.Equal(t,
		[]string{"_eq", "_neq", "_contains", "_like", "_ilike", "_nlike", "_gt", "_lt", "_is_null"},
		e.GetSupportedOperators(),
	)
}

func TestElasticSaasSource_GetQuery(t *testing.T) {
	var e ElasticSaasSource
	req := FetchLogRequest{
		QueryRequest: LogsQueryBuilderRequest{
			Where: query.QueryWhereClause{
				Binary: query.BinaryWhereClause{
					"service": {query.Eq: "api-server"},
				},
			},
		},
	}
	got, err := e.GetQuery(nil, req)
	assert.NoError(t, err)
	// Delegates to buildESQueryFromWhere -> valid DSL wrapping the field.
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal([]byte(got), &parsed))
	assert.Contains(t, parsed, "query")
	assert.Contains(t, got, "service")
	assert.Contains(t, got, "api-server")
}

// ---------- esLogTimeRangeClause ----------

func TestESLogTimeRangeClause(t *testing.T) {
	rng := func(m map[string]any) map[string]any {
		return m["range"].(map[string]any)["@timestamp"].(map[string]any)
	}
	t.Run("both bounds", func(t *testing.T) {
		ts := rng(esLogTimeRangeClause(1000, 2000))
		assert.Equal(t, int64(1000), ts["gte"])
		assert.Equal(t, int64(2000), ts["lte"])
		assert.Equal(t, "epoch_millis", ts["format"])
	})
	t.Run("start only", func(t *testing.T) {
		ts := rng(esLogTimeRangeClause(1000, 0))
		assert.Equal(t, int64(1000), ts["gte"])
		_, hasLte := ts["lte"]
		assert.False(t, hasLte)
	})
	t.Run("end only", func(t *testing.T) {
		ts := rng(esLogTimeRangeClause(0, 2000))
		assert.Equal(t, int64(2000), ts["lte"])
		_, hasGte := ts["gte"]
		assert.False(t, hasGte)
	})
	t.Run("neither", func(t *testing.T) {
		ts := rng(esLogTimeRangeClause(0, 0))
		assert.Equal(t, "epoch_millis", ts["format"])
		assert.Len(t, ts, 1)
	})
}

// buildESKQLQueryBody and the KQL translator are covered in elasticsearch_kql_test.go.

// ---------- parseESSearchLogs ----------

func TestParseESSearchLogs(t *testing.T) {
	t.Run("parses hits and drops unparseable", func(t *testing.T) {
		raw := `{"hits":{"hits":[
			{"_source":{"@timestamp":"2024-01-01T00:00:00Z","log":"boom","stream":"stderr"}},
			{"_source":{"log":"no timestamp -> dropped"}}
		]}}`
		logs, err := parseESSearchLogs(raw)
		assert.NoError(t, err)
		assert.Len(t, logs, 1)
		assert.Equal(t, "boom", logs[0].Message)
		assert.Equal(t, "ERROR", logs[0].Severity) // stderr -> ERROR
	})
	t.Run("empty hits", func(t *testing.T) {
		logs, err := parseESSearchLogs(`{"hits":{"hits":[]}}`)
		assert.NoError(t, err)
		assert.Empty(t, logs)
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := parseESSearchLogs(`{not-json`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal DSL response")
	})
}

// ---------- parseESPPLLogs ----------

func TestParseESPPLLogs(t *testing.T) {
	t.Run("zips schema columns to rows", func(t *testing.T) {
		raw := `{
			"schema":[{"name":"@timestamp","type":"string"},{"name":"log","type":"string"},{"name":"stream","type":"string"}],
			"datarows":[["2024-01-01T00:00:00Z","hello","stdout"],["2024-01-01T00:00:01Z","","stdout"]]
		}`
		logs, err := parseESPPLLogs(raw)
		assert.NoError(t, err)
		assert.Len(t, logs, 2) // current parser preserves the structured row even when its log field is empty
		assert.Equal(t, "hello", logs[0].Message)
		assert.Equal(t, "INFO", logs[0].Severity) // stdout -> INFO
	})
	t.Run("extra row values beyond schema are ignored", func(t *testing.T) {
		raw := `{"schema":[{"name":"@timestamp"},{"name":"log"}],"datarows":[["2024-01-01T00:00:00Z","hi","EXTRA"]]}`
		logs, err := parseESPPLLogs(raw)
		assert.NoError(t, err)
		assert.Len(t, logs, 1)
		assert.Equal(t, "hi", logs[0].Message)
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := parseESPPLLogs(`nope`)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal PPL response")
	})
}

// ---------- parseESLabelValuesResponse ----------

func TestParseESLabelValuesResponse(t *testing.T) {
	t.Run("collects bucket keys, skips empty", func(t *testing.T) {
		raw := []byte(`{"aggregations":{"values":{"buckets":[{"key":"a"},{"key":200},{"key":""},"not-a-map"]}}}`)
		vals, err := parseESLabelValuesResponse(raw)
		assert.NoError(t, err)
		got := make([]string, 0, len(vals))
		for _, v := range vals {
			got = append(got, v.Value)
		}
		assert.Equal(t, []string{"a", "200"}, got) // empty key skipped, non-map bucket skipped
	})
	t.Run("missing aggregations", func(t *testing.T) {
		_, err := parseESLabelValuesResponse([]byte(`{}`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "aggregations")
	})
	t.Run("missing values", func(t *testing.T) {
		_, err := parseESLabelValuesResponse([]byte(`{"aggregations":{}}`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "'values'")
	})
	t.Run("missing buckets", func(t *testing.T) {
		_, err := parseESLabelValuesResponse([]byte(`{"aggregations":{"values":{}}}`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "buckets")
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := parseESLabelValuesResponse([]byte(`x`))
		assert.Error(t, err)
	})
}

// ---------- parseESMappingFields / extractFieldsFromProperties ----------

func fieldSet(fields []OutputLogLabelFields) map[string]any {
	m := make(map[string]any, len(fields))
	for _, f := range fields {
		m[f.Field] = f.Attributes["type"]
	}
	return m
}

func TestParseESMappingFields(t *testing.T) {
	t.Run("unwraps index -> mappings -> properties incl multi-fields", func(t *testing.T) {
		raw := []byte(`{"logs-000001":{"mappings":{"properties":{
			"level":{"type":"keyword"},
			"msg":{"type":"text","fields":{"keyword":{"type":"keyword"}}},
			"resource":{"properties":{"k8s":{"type":"keyword"}}}
		}}}}`)
		fields, err := parseESMappingFields(raw)
		assert.NoError(t, err)
		set := fieldSet(fields)
		assert.Equal(t, "keyword", set["level"])
		assert.Equal(t, "text", set["msg"])
		assert.Equal(t, "keyword", set["msg.keyword"])  // multi-field emitted
		assert.Equal(t, "keyword", set["resource.k8s"]) // nested object flattened
	})
	t.Run("no properties -> nil", func(t *testing.T) {
		fields, err := parseESMappingFields([]byte(`{"idx":{"mappings":{}}}`))
		assert.NoError(t, err)
		assert.Nil(t, fields)
	})
	t.Run("empty object -> nil", func(t *testing.T) {
		fields, err := parseESMappingFields([]byte(`{}`))
		assert.NoError(t, err)
		assert.Nil(t, fields)
	})
	t.Run("invalid json", func(t *testing.T) {
		_, err := parseESMappingFields([]byte(`{`))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal mapping response")
	})
}

func TestExtractFieldsFromProperties(t *testing.T) {
	props := map[string]any{
		"level":   map[string]any{"type": "keyword"},
		"msg":     map[string]any{"type": "text", "fields": map[string]any{"keyword": map[string]any{"type": "keyword"}}},
		"nested":  map[string]any{"properties": map[string]any{"inner": map[string]any{"type": "long"}}},
		"garbage": "not-a-map", // skipped
	}
	set := fieldSet(extractFieldsFromProperties(props, ""))
	assert.Equal(t, "keyword", set["level"])
	assert.Equal(t, "text", set["msg"])
	assert.Equal(t, "keyword", set["msg.keyword"])
	assert.Equal(t, "long", set["nested.inner"])
	_, hasGarbage := set["garbage"]
	assert.False(t, hasGarbage)

	// A non-empty prefix qualifies every emitted field name.
	set2 := fieldSet(extractFieldsFromProperties(map[string]any{"a": map[string]any{"type": "keyword"}}, "pfx"))
	_, ok := set2["pfx.a"]
	assert.True(t, ok)
}
