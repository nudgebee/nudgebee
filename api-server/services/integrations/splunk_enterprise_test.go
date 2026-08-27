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

// splunkEnterpriseCfg builds a config slice from a convenience map.
func splunkEnterpriseCfg(m map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(m))
	for k, v := range m {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

// validSplunkEnterpriseCfg returns a minimal valid token-auth config. Callers override
// individual keys before passing into ValidateConfig / TestConnection.
func validSplunkEnterpriseCfg() map[string]string {
	return map[string]string{
		SplunkEnterpriseConfigURL:      "https://splunk.example.com:8089",
		SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthToken,
		SplunkEnterpriseConfigToken:    "a-token",
	}
}

// ----- metadata / schema ----------------------------------------------------

func TestSplunkEnterprise_Name(t *testing.T) {
	assert.Equal(t, "splunk_enterprise", SplunkEnterprise{}.Name())
}

// The two Splunk integrations must never collide: splunk_observability_platform is the
// SignalFx SaaS product, and eventrule provider resolution already maps the bare string
// "splunk" onto it.
func TestSplunkEnterprise_NameIsDistinctFromObservabilityCloud(t *testing.T) {
	assert.NotEqual(t, IntegrationSplunkO11y, IntegrationSplunkEnterprise)
	assert.NotEqual(t, IntegrationSplunkWebhook, IntegrationSplunkEnterprise)
}

func TestSplunkEnterprise_Category(t *testing.T) {
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, SplunkEnterprise{}.Category())
}

func TestSplunkEnterprise_ConfigSchema_RequiredFields(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	assert.True(t, schema.Testable, "schema must declare Testable=true")
	assert.Contains(t, schema.Required, SplunkEnterpriseConfigURL)
	assert.Contains(t, schema.Required, SplunkEnterpriseConfigAuthType)
}

func TestSplunkEnterprise_ConfigSchema_PropertiesExist(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	wantKeys := []string{
		SplunkEnterpriseConfigURL, SplunkEnterpriseConfigAuthType,
		SplunkEnterpriseConfigToken, SplunkEnterpriseConfigUsername, SplunkEnterpriseConfigPassword,
		SplunkEnterpriseConfigLogIndex, SplunkEnterpriseConfigApp, SplunkEnterpriseConfigInsecure,
		core.IntegrationConfigName, core.AccountId, core.DefaultLogProvider,
	}
	for _, key := range wantKeys {
		_, ok := schema.Properties[key]
		assert.True(t, ok, "schema.Properties must contain %q", key)
	}
}

// Phase 1 ships logs only. Advertising a default trace or metrics provider toggle would
// let a user route those signals at a source that does not exist.
// The provider toggles must track what is actually implemented. Offering a toggle with
// no source behind it produces a view that errors on open, which reads to the user as a
// broken integration rather than an unsupported one.
func TestSplunkEnterprise_ConfigSchema_ProviderTogglesMatchImplementedSources(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()

	_, hasLogs := schema.Properties[core.DefaultLogProvider]
	assert.True(t, hasLogs, "SplunkEnterpriseLogSource exists")

	_, hasMetrics := schema.Properties[core.DefaultMetricsProvider]
	assert.True(t, hasMetrics, "SplunkEnterpriseMetricSource exists")

	// Splunk Enterprise has no native trace store — spans only exist there if the OTel
	// collector was pointed at a traces index — and no SplunkEnterpriseTraceSource is
	// implemented. Flip this when one is.
	_, hasTrace := schema.Properties[core.DefaultTraceProvider]
	assert.False(t, hasTrace, "no trace source exists yet")
}

// Metrics are opt-in per install: a metrics index must be created explicitly with
// datatype=metric, so there is no safe default to guess. An empty value must therefore
// remain valid rather than being coerced to a name that does not exist.
func TestSplunkEnterprise_MetricIndexDefaultsToEmpty(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	prop, ok := schema.Properties[SplunkEnterpriseConfigMetricIndex]
	assert.True(t, ok, "metric index must be configurable")
	assert.Equal(t, SplunkEnterpriseDefaultMetricIndex, prop.Default)
	assert.NotContains(t, schema.Required, SplunkEnterpriseConfigMetricIndex,
		"a logs-only Splunk must still be configurable")
}

func TestSplunkEnterprise_ConfigSchema_SecretsAreEncrypted(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	assert.True(t, schema.Properties[SplunkEnterpriseConfigToken].IsEncrypted)
	assert.True(t, schema.Properties[SplunkEnterpriseConfigPassword].IsEncrypted)

	// The index and app are routing, not credentials — encrypting them would break the
	// plaintext reads in splunkEnterpriseConfigFromValues.
	assert.False(t, schema.Properties[SplunkEnterpriseConfigLogIndex].IsEncrypted)
	assert.False(t, schema.Properties[SplunkEnterpriseConfigApp].IsEncrypted)
}

func TestSplunkEnterprise_ConfigSchema_IndexAndAppHaveDefaults(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	assert.NotContains(t, schema.Required, SplunkEnterpriseConfigLogIndex)
	assert.NotContains(t, schema.Required, SplunkEnterpriseConfigApp)
	assert.Equal(t, SplunkEnterpriseDefaultLogIndex, schema.Properties[SplunkEnterpriseConfigLogIndex].Default)
	assert.Equal(t, SplunkEnterpriseDefaultApp, schema.Properties[SplunkEnterpriseConfigApp].Default)
}

// ----- URL normalization ----------------------------------------------------

// The port is the whole point for Splunk: 8089 is the management API and 8000 is the web
// UI. A normalizer that dropped it (as a hostname-only one would) would silently point
// every search at the wrong listener.
func TestNormalizeSplunkEnterpriseURL_PreservesPort(t *testing.T) {
	assert.Equal(t, "https://splunk.example.com:8089", NormalizeSplunkEnterpriseURL("https://splunk.example.com:8089"))
	assert.Equal(t, "https://splunk.example.com:8089", NormalizeSplunkEnterpriseURL("  https://splunk.example.com:8089/  "))
	assert.Equal(t, "https://splunk.example.com:8089", NormalizeSplunkEnterpriseURL("https://splunk.example.com:8089/en-US/app/search"))
}

func TestNormalizeSplunkEnterpriseURL_EdgeCases(t *testing.T) {
	assert.Equal(t, "", NormalizeSplunkEnterpriseURL(""))
	assert.Equal(t, "", NormalizeSplunkEnterpriseURL("   "))
	assert.Equal(t, "http://localhost:8089", NormalizeSplunkEnterpriseURL("http://localhost:8089"))
	// No scheme: left alone (ValidateConfig is what rejects it) but trailing slashes go.
	assert.Equal(t, "splunk.example.com", NormalizeSplunkEnterpriseURL("splunk.example.com/"))
}

// ----- index-name safety ----------------------------------------------------

func TestIsSafeSplunkIndexName(t *testing.T) {
	for _, name := range []string{"main", "otel_logs", "k8s-logs", "_internal", "_audit", "idx123"} {
		assert.True(t, IsSafeSplunkIndexName(name), "%q should be accepted", name)
	}
	for _, name := range []string{
		"",
		`main" | delete`, // the injection this guard exists for
		"main index",
		"main|delete",
		"main'",
		"main;drop",
		"main\n",
		"main*",
		strings.Repeat("a", 256),
	} {
		assert.False(t, IsSafeSplunkIndexName(name), "%q should be rejected", name)
	}
}

// ----- ValidateConfig -------------------------------------------------------

func TestSplunkEnterprise_ValidateConfig_Valid(t *testing.T) {
	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(validSplunkEnterpriseCfg()), "acct")
	assert.Empty(t, errs)
}

func TestSplunkEnterprise_ValidateConfig_ValidBasicAuth(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	delete(cfg, SplunkEnterpriseConfigToken)
	cfg[SplunkEnterpriseConfigAuthType] = SplunkEnterpriseAuthBasic
	cfg[SplunkEnterpriseConfigUsername] = "nudgebee"
	cfg[SplunkEnterpriseConfigPassword] = "secret"

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	assert.Empty(t, errs)
}

func TestSplunkEnterprise_ValidateConfig_MissingURL(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	delete(cfg, SplunkEnterpriseConfigURL)

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), SplunkEnterpriseConfigURL)
}

func TestSplunkEnterprise_ValidateConfig_URLWithoutScheme(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigURL] = "splunk.example.com:8089"

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "http://")
}

func TestSplunkEnterprise_ValidateConfig_URLWithPath(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigURL] = "https://splunk.example.com:8089/en-US/app/search"

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "base URL only")
}

func TestSplunkEnterprise_ValidateConfig_TokenMissing(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	delete(cfg, SplunkEnterpriseConfigToken)

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), SplunkEnterpriseConfigToken)
}

func TestSplunkEnterprise_ValidateConfig_BasicAuthMissingCredentials(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	delete(cfg, SplunkEnterpriseConfigToken)
	cfg[SplunkEnterpriseConfigAuthType] = SplunkEnterpriseAuthBasic

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 2)
}

func TestSplunkEnterprise_ValidateConfig_UnknownAuthType(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigAuthType] = "oauth"

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "oauth")
}

func TestSplunkEnterprise_ValidateConfig_RejectsUnsafeIndex(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigLogIndex] = `otel_logs" | delete`

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), SplunkEnterpriseConfigLogIndex)
}

func TestSplunkEnterprise_ValidateConfig_RejectsUnsafeApp(t *testing.T) {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigApp] = "search/../../etc"

	errs := SplunkEnterprise{}.ValidateConfig(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), SplunkEnterpriseConfigApp)
}

// ----- resolved config ------------------------------------------------------

func TestSplunkEnterpriseConfigFromValues_AppliesDefaults(t *testing.T) {
	cfg, err := splunkEnterpriseConfigFromValues(splunkEnterpriseCfg(validSplunkEnterpriseCfg()))
	require.NoError(t, err)

	assert.Equal(t, SplunkEnterpriseDefaultLogIndex, cfg.LogIndex)
	assert.Equal(t, SplunkEnterpriseDefaultApp, cfg.App)
	assert.Equal(t, SplunkEnterpriseAuthToken, cfg.AuthType)
	assert.False(t, cfg.InsecureSkipVerify)
}

// A blank stored value must fall back to the default rather than producing index="".
func TestSplunkEnterpriseConfigFromValues_BlankIndexFallsBackToDefault(t *testing.T) {
	raw := validSplunkEnterpriseCfg()
	raw[SplunkEnterpriseConfigLogIndex] = "   "
	raw[SplunkEnterpriseConfigApp] = ""

	cfg, err := splunkEnterpriseConfigFromValues(splunkEnterpriseCfg(raw))
	require.NoError(t, err)
	assert.Equal(t, SplunkEnterpriseDefaultLogIndex, cfg.LogIndex)
	assert.Equal(t, SplunkEnterpriseDefaultApp, cfg.App)
}

func TestSplunkEnterpriseConfigFromValues_RejectsUnsafeIndex(t *testing.T) {
	raw := validSplunkEnterpriseCfg()
	raw[SplunkEnterpriseConfigLogIndex] = `otel_logs" | delete`

	_, err := splunkEnterpriseConfigFromValues(splunkEnterpriseCfg(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), SplunkEnterpriseConfigLogIndex)
}

func TestSplunkEnterpriseConfigFromValues_InsecureFlag(t *testing.T) {
	raw := validSplunkEnterpriseCfg()
	raw[SplunkEnterpriseConfigInsecure] = "true"

	cfg, err := splunkEnterpriseConfigFromValues(splunkEnterpriseCfg(raw))
	require.NoError(t, err)
	assert.True(t, cfg.InsecureSkipVerify)
}

func TestSplunkEnterpriseConfig_AuthHeaders(t *testing.T) {
	bearer := SplunkEnterpriseConfig{AuthType: SplunkEnterpriseAuthToken, Token: "tok"}
	assert.Equal(t, "Bearer tok", bearer.AuthHeaders()["Authorization"])

	basic := SplunkEnterpriseConfig{AuthType: SplunkEnterpriseAuthBasic, Username: "user", Password: "pass"}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	assert.Equal(t, want, basic.AuthHeaders()["Authorization"])
}

func TestSplunkEnterpriseConfig_SearchEndpoint(t *testing.T) {
	cfg := SplunkEnterpriseConfig{URL: "https://splunk.example.com:8089", App: "search"}
	assert.Equal(t, "https://splunk.example.com:8089/servicesNS/-/search/search/v2/jobs", cfg.SearchEndpoint())
}

// ----- TestConnection -------------------------------------------------------

func TestSplunkEnterprise_TestConnection_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, SplunkEnterpriseServerInfoPath, r.URL.Path)
		assert.Equal(t, "Bearer a-token", r.Header.Get("Authorization"))
		assert.Equal(t, "json", r.URL.Query().Get("output_mode"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigURL] = server.URL

	err := SplunkEnterprise{}.TestConnection(nil, splunkEnterpriseCfg(cfg), "acct")
	assert.NoError(t, err)
}

func TestSplunkEnterprise_TestConnection_BasicAuthHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "nudgebee", user)
		assert.Equal(t, "secret", pass)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := map[string]string{
		SplunkEnterpriseConfigURL:      server.URL,
		SplunkEnterpriseConfigAuthType: SplunkEnterpriseAuthBasic,
		SplunkEnterpriseConfigUsername: "nudgebee",
		SplunkEnterpriseConfigPassword: "secret",
	}

	err := SplunkEnterprise{}.TestConnection(nil, splunkEnterpriseCfg(cfg), "acct")
	assert.NoError(t, err)
}

func TestSplunkEnterprise_TestConnection_StatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "401"},
		{http.StatusForbidden, "403"},
		{http.StatusNotFound, "8089"},
		{http.StatusInternalServerError, "500"},
	}

	for _, tc := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))

		cfg := validSplunkEnterpriseCfg()
		cfg[SplunkEnterpriseConfigURL] = server.URL

		err := SplunkEnterprise{}.TestConnection(nil, splunkEnterpriseCfg(cfg), "acct")
		require.Error(t, err, "status %d must be an error", tc.status)
		assert.Contains(t, err.Error(), tc.want)

		server.Close()
	}
}
