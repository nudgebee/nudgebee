package integrations

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openobserveCfg builds a config slice from a convenience map.
func openobserveCfg(m map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(m))
	for k, v := range m {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

// validOpenObserveCfg returns a minimal valid config. Callers can override
// individual keys before passing into ValidateConfig / TestConnection.
func validOpenObserveCfg() map[string]string {
	return map[string]string{
		"openobserve_url":      "https://cloud.openobserve.ai",
		"openobserve_org_id":   "default",
		"openobserve_username": "admin@example.com",
		"openobserve_password": "secret",
	}
}

// ----- metadata / schema ----------------------------------------------------

func TestOpenObserve_Name(t *testing.T) {
	assert.Equal(t, "openobserve", OpenObserve{}.Name())
}

func TestOpenObserve_Category(t *testing.T) {
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, OpenObserve{}.Category())
}

func TestOpenObserve_ConfigSchema_RequiredFields(t *testing.T) {
	schema := OpenObserve{}.ConfigSchema()
	assert.True(t, schema.Testable, "schema must declare Testable=true")
	for _, req := range []string{"openobserve_url", "openobserve_org_id", "openobserve_username", "openobserve_password"} {
		assert.Contains(t, schema.Required, req)
	}
}

func TestOpenObserve_ConfigSchema_PropertiesExist(t *testing.T) {
	schema := OpenObserve{}.ConfigSchema()
	wantKeys := []string{
		"openobserve_url", "openobserve_org_id",
		"openobserve_username", "openobserve_password",
		"openobserve_log_stream", "openobserve_trace_stream",
		core.IntegrationConfigName, core.AccountId,
		core.DefaultLogProvider, core.DefaultTraceProvider, core.DefaultMetricsProvider,
	}
	for _, key := range wantKeys {
		_, ok := schema.Properties[key]
		assert.True(t, ok, "schema.Properties must contain %q", key)
	}
}

// Stream names are optional: an integration configured before the fields existed, or one
// where the user left them blank, must still resolve to OpenObserve's default stream
// rather than emitting FROM "".
func TestOpenObserve_ConfigSchema_StreamsAreOptionalWithDefaults(t *testing.T) {
	schema := OpenObserve{}.ConfigSchema()

	assert.NotContains(t, schema.Required, "openobserve_log_stream")
	assert.NotContains(t, schema.Required, "openobserve_trace_stream")
	assert.Equal(t, OpenObserveDefaultLogStream, schema.Properties["openobserve_log_stream"].Default)
	assert.Equal(t, OpenObserveDefaultTraceStream, schema.Properties["openobserve_trace_stream"].Default)

	// Streams are not credentials — they must not be encrypted at rest.
	assert.False(t, schema.Properties["openobserve_log_stream"].IsEncrypted)
	assert.False(t, schema.Properties["openobserve_trace_stream"].IsEncrypted)
}

func TestOpenObserve_ConfigSchema_PasswordIsEncrypted(t *testing.T) {
	schema := OpenObserve{}.ConfigSchema()
	assert.True(t, schema.Properties["openobserve_password"].IsEncrypted)
}

func TestOpenObserve_ImplementsTestableIntegration(t *testing.T) {
	var _ core.TestableIntegration = OpenObserve{}
}

// ----- stream-name safety ----------------------------------------------------

// Stream names land in SQL as bare identifiers inside FROM "<stream>", a position that
// cannot be parameterized. Anything able to close that quote is an injection vector.
func TestIsSafeOpenObserveStreamName(t *testing.T) {
	valid := []string{"default", "k8s_logs", "otel_spans", "Stream1", "_leading", "a"}
	for _, s := range valid {
		assert.True(t, IsSafeOpenObserveStreamName(s), "%q should be accepted", s)
	}

	invalid := map[string]string{
		"empty":             "",
		"quote breakout":    `default" UNION SELECT * FROM secrets --`,
		"trailing quote":    `default"`,
		"semicolon":         "default; DROP TABLE users",
		"space":             "my stream",
		"hyphen":            "k8s-logs",
		"dot traversal":     "../other",
		"backtick":          "default`",
		"single quote":      "default'",
		"newline":           "default\nDROP",
		"comment":           "default--",
		"too long":          strings.Repeat("a", 256),
		"unicode homoglyph": "defaulт", // Cyrillic 'т'
	}
	for name, s := range invalid {
		assert.False(t, IsSafeOpenObserveStreamName(s), "%s: %q should be rejected", name, s)
	}
}

// ----- ValidateConfig (structural, no I/O) ----------------------------------

func TestOpenObserve_ValidateConfig_AllFieldsValid(t *testing.T) {
	errs := OpenObserve{}.ValidateConfig(nil, openobserveCfg(validOpenObserveCfg()), "acc-1")
	assert.Empty(t, errs)
}

func TestOpenObserve_ValidateConfig_MissingAllFields(t *testing.T) {
	errs := OpenObserve{}.ValidateConfig(nil, []core.IntegrationConfigValue{}, "acc-1")
	require.NotEmpty(t, errs)
	joined := joinErrors(errs)
	assert.Contains(t, joined, "openobserve_url is required")
	assert.Contains(t, joined, "openobserve_org_id is required")
	assert.Contains(t, joined, "openobserve_username is required")
	assert.Contains(t, joined, "openobserve_password is required")
}

func TestOpenObserve_ValidateConfig_MissingIndividualFields(t *testing.T) {
	cases := []struct {
		name    string
		omit    string
		wantMsg string
	}{
		{"url", "openobserve_url", "openobserve_url is required"},
		{"org_id", "openobserve_org_id", "openobserve_org_id is required"},
		{"username", "openobserve_username", "openobserve_username is required"},
		{"password", "openobserve_password", "openobserve_password is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validOpenObserveCfg()
			delete(cfg, tc.omit)
			errs := OpenObserve{}.ValidateConfig(nil, openobserveCfg(cfg), "acc-1")
			require.NotEmpty(t, errs)
			assert.Contains(t, joinErrors(errs), tc.wantMsg)
		})
	}
}

func TestOpenObserve_ValidateConfig_URLSchemeValidation(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantMsg string
	}{
		{"ftp scheme", "ftp://openobserve.example.com", "must start with http:// or https://"},
		{"no scheme", "openobserve.example.com", "must start with http:// or https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validOpenObserveCfg()
			cfg["openobserve_url"] = tc.url
			errs := OpenObserve{}.ValidateConfig(nil, openobserveCfg(cfg), "acc-1")
			require.NotEmpty(t, errs)
			assert.Contains(t, joinErrors(errs), tc.wantMsg)
		})
	}
}

func TestOpenObserve_ValidateConfig_URLWithPathRejected(t *testing.T) {
	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = "https://cloud.openobserve.ai/web/logs"
	errs := OpenObserve{}.ValidateConfig(nil, openobserveCfg(cfg), "acc-1")
	require.NotEmpty(t, errs)
	assert.Contains(t, joinErrors(errs), "must be the base URL only")
}

func TestOpenObserve_ValidateConfig_WhitespaceIsTrimmed(t *testing.T) {
	cfg := map[string]string{
		"openobserve_url":      "  https://cloud.openobserve.ai  ",
		"openobserve_org_id":   "  default  ",
		"openobserve_username": "  admin@example.com  ",
		"openobserve_password": "secret",
	}
	errs := OpenObserve{}.ValidateConfig(nil, openobserveCfg(cfg), "acc-1")
	assert.Empty(t, errs, "whitespace-padded values should pass validation")
}

// ----- normalizeOpenObserveURL ----------------------------------------------

func TestNormalizeOpenObserveURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"base only", "https://cloud.openobserve.ai", "https://cloud.openobserve.ai"},
		{"trailing slash", "https://cloud.openobserve.ai/", "https://cloud.openobserve.ai"},
		{"with path", "https://cloud.openobserve.ai/web/logs", "https://cloud.openobserve.ai"},
		{"with query", "https://cloud.openobserve.ai?org=default", "https://cloud.openobserve.ai"},
		{"with port", "http://localhost:5080", "http://localhost:5080"},
		{"with port and path", "http://localhost:5080/api/default/streams", "http://localhost:5080"},
		{"leading whitespace", "  https://cloud.openobserve.ai  ", "https://cloud.openobserve.ai"},
		{"schemeless fallback", "not-a-url/", "not-a-url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeOpenObserveURL(tc.raw))
		})
	}
}

// ----- TestConnection (live connectivity via httptest) -----------------------

func TestOpenObserve_TestConnection_HappyPath(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"list":[]}`))
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	assert.NoError(t, err)
	assert.Equal(t, "/api/default/streams", gotPath)

	// Verify Basic auth header is well-formed.
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin@example.com:secret"))
	assert.Equal(t, wantAuth, gotAuth)
}

func TestOpenObserve_TestConnection_OrgIdPathEscaped(t *testing.T) {
	var gotRawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// r.URL.Path is auto-decoded by Go; use RawPath to verify encoding.
		gotRawPath = r.URL.RawPath
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL
	cfg["openobserve_org_id"] = "my org/test"

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	assert.NoError(t, err)
	// net/url.PathEscape encodes space as %20 and / as %2F.
	assert.Equal(t, "/api/my%20org%2Ftest/streams", gotRawPath)
}

func TestOpenObserve_TestConnection_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`Unauthorized`))
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
	assert.Contains(t, err.Error(), "check username and password")
}

func TestOpenObserve_TestConnection_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

func TestOpenObserve_TestConnection_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "openobserve_url")
}

func TestOpenObserve_TestConnection_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 503")
}

func TestOpenObserve_TestConnection_UnreachableHost(t *testing.T) {
	// 127.0.0.1:1 is reliably unreachable; matches the hive_test convention.
	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = "http://127.0.0.1:1"

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to connect to OpenObserve")
}

func TestOpenObserve_TestConnection_URLNormalized(t *testing.T) {
	// Even if the URL has a trailing path, TestConnection normalizes it
	// before probing, so the request still hits /api/{org}/streams.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := validOpenObserveCfg()
	cfg["openobserve_url"] = srv.URL + "/web/logs"

	err := OpenObserve{}.TestConnection(nil, openobserveCfg(cfg), "acc-1")
	assert.NoError(t, err)
	assert.True(t, strings.HasSuffix(gotPath, "/api/default/streams"),
		"expected path to end with /api/default/streams, got %s", gotPath)
}
