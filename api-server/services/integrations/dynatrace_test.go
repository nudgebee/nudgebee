package integrations

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/integrations/core"
)

// newDTValidateServer creates a test server that responds to any POST with the given status and body,
// simulating the Grail /platform/storage/query/v1/query:execute endpoint.
func newDTValidateServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
}

// ---- Integration struct methods ----

func TestDynatrace_Name(t *testing.T) {
	d := Dynatrace{}
	assert.Equal(t, IntegrationDynatrace, d.Name())
}

func TestDynatrace_Category(t *testing.T) {
	d := Dynatrace{}
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, d.Category())
}

func TestDynatrace_ConfigSchema(t *testing.T) {
	d := Dynatrace{}
	schema := d.ConfigSchema()

	// Required fields present
	assert.Contains(t, schema.Required, DynatraceConfigApiToken)
	assert.Contains(t, schema.Required, DynatraceConfigBaseUrl)

	// Properties exist
	assert.Contains(t, schema.Properties, DynatraceConfigApiToken)
	assert.Contains(t, schema.Properties, DynatraceConfigBaseUrl)
	assert.Contains(t, schema.Properties, core.DefaultLogProvider)
	assert.Contains(t, schema.Properties, core.DefaultTraceProvider)
	assert.Contains(t, schema.Properties, core.DefaultMetricsProvider)

	// Token must be encrypted, URL must not be
	assert.True(t, schema.Properties[DynatraceConfigApiToken].IsEncrypted, "api_token should be encrypted")
	assert.False(t, schema.Properties[DynatraceConfigBaseUrl].IsEncrypted, "base_url should not be encrypted")
}

// ---- ValidateDynatraceConfig — guard clauses ----

func TestDynatrace_Validate_EmptyToken(t *testing.T) {
	d := Dynatrace{}
	err := d.ValidateDynatraceConfig("", "https://example.apps.dynatrace.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_token")
}

func TestDynatrace_Validate_EmptyBaseURL(t *testing.T) {
	d := Dynatrace{}
	err := d.ValidateDynatraceConfig("sometoken", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

// ---- ValidateDynatraceConfig — HTTP status codes ----

func TestDynatrace_Validate_HTTP200(t *testing.T) {
	srv := newDTValidateServer(t, http.StatusOK, `{"state":"SUCCEEDED","result":{"records":[]}}`)
	defer srv.Close()

	d := Dynatrace{}
	assert.NoError(t, d.ValidateDynatraceConfig("test-token", srv.URL))
}

func TestDynatrace_Validate_HTTP202(t *testing.T) {
	// 202 Accepted is also treated as success (async poll started)
	srv := newDTValidateServer(t, http.StatusAccepted, `{"state":"RUNNING","requestToken":"abc"}`)
	defer srv.Close()

	d := Dynatrace{}
	assert.NoError(t, d.ValidateDynatraceConfig("test-token", srv.URL))
}

func TestDynatrace_Validate_HTTP401(t *testing.T) {
	srv := newDTValidateServer(t, http.StatusUnauthorized, `{"error":"Unauthorized"}`)
	defer srv.Close()

	d := Dynatrace{}
	err := d.ValidateDynatraceConfig("bad-token", srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bearer token")
}

func TestDynatrace_Validate_HTTP403(t *testing.T) {
	srv := newDTValidateServer(t, http.StatusForbidden, `{"error":"Forbidden","message":"missing scopes"}`)
	defer srv.Close()

	d := Dynatrace{}
	err := d.ValidateDynatraceConfig("limited-token", srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scopes")
}

func TestDynatrace_Validate_HTTP500(t *testing.T) {
	srv := newDTValidateServer(t, http.StatusInternalServerError, `{"error":"Internal Server Error"}`)
	defer srv.Close()

	d := Dynatrace{}
	err := d.ValidateDynatraceConfig("test-token", srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestDynatrace_Validate_TrailingSlash(t *testing.T) {
	// The implementation calls strings.TrimRight(baseURL, "/") — a trailing slash must still work.
	srv := newDTValidateServer(t, http.StatusOK, `{"state":"SUCCEEDED","result":{"records":[]}}`)
	defer srv.Close()

	d := Dynatrace{}
	assert.NoError(t, d.ValidateDynatraceConfig("test-token", srv.URL+"/"))
}

func TestDynatrace_Validate_NetworkError(t *testing.T) {
	d := Dynatrace{}
	// Port 1 should be unreachable on any reasonable test machine
	err := d.ValidateDynatraceConfig("test-token", "http://127.0.0.1:1")
	require.Error(t, err)
}

// ---- Integration test (real Dynatrace Grail) ----

// TestDynatrace_Validate_Integration calls the real Grail endpoint.
// Set DT_TOKEN and DT_BASE_URL environment variables to run this test.
func TestDynatrace_Validate_Integration(t *testing.T) {
	token := os.Getenv("DT_TOKEN")
	baseURL := os.Getenv("DT_BASE_URL")
	if token == "" || baseURL == "" {
		t.Skip("DT_TOKEN and DT_BASE_URL env vars required for integration test")
	}

	d := Dynatrace{}
	err := d.ValidateDynatraceConfig(token, baseURL)
	assert.NoError(t, err, "validation should succeed with real credentials")
}
