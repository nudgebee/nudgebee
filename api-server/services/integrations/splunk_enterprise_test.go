package integrations

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
		SplunkEnterpriseConfigLogIndex, SplunkEnterpriseConfigMetricIndex, SplunkEnterpriseConfigTraceIndex,
		SplunkEnterpriseConfigApp, SplunkEnterpriseConfigInsecure,
		core.IntegrationConfigName, core.AccountId, core.DefaultLogProvider,
	}
	for _, key := range wantKeys {
		_, ok := schema.Properties[key]
		assert.True(t, ok, "schema.Properties must contain %q", key)
	}
}

// The provider toggles must track what is actually implemented. Offering a toggle with
// no source behind it produces a view that errors on open, which reads to the user as a
// broken integration rather than an unsupported one — and the reverse (a source with no
// toggle) leaves the signal unreachable no matter how it is configured.
func TestSplunkEnterprise_ConfigSchema_ProviderTogglesMatchImplementedSources(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()

	_, hasLogs := schema.Properties[core.DefaultLogProvider]
	assert.True(t, hasLogs, "SplunkEnterpriseLogSource exists")

	_, hasMetrics := schema.Properties[core.DefaultMetricsProvider]
	assert.True(t, hasMetrics, "SplunkEnterpriseMetricSource exists")

	_, hasTrace := schema.Properties[core.DefaultTraceProvider]
	assert.True(t, hasTrace, "SplunkEnterpriseTraceSource exists")
}

// Traces are opt-in per install for a stronger reason than metrics: Splunk Enterprise has
// no trace store at all, so spans exist only where an OTel Collector was explicitly
// pointed at an index. An empty value must stay valid, or a logs-only Splunk stops being
// configurable.
func TestSplunkEnterprise_TraceIndexDefaultsToEmpty(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	prop, ok := schema.Properties[SplunkEnterpriseConfigTraceIndex]
	assert.True(t, ok, "trace index must be configurable")
	assert.Equal(t, SplunkEnterpriseDefaultTraceIndex, prop.Default)
	assert.NotContains(t, schema.Required, SplunkEnterpriseConfigTraceIndex,
		"a logs-only Splunk must still be configurable")
}

// Every index field is interpolated into SPL as a bare token, so a bad name has to be
// rejected next to the field that caused it rather than surfacing later as an opaque
// failure on whichever tab happens to use that signal.
func TestSplunkEnterprise_ValidateConfig_RejectsUnsafeIndexNames(t *testing.T) {
	for _, key := range []string{
		SplunkEnterpriseConfigLogIndex,
		SplunkEnterpriseConfigMetricIndex,
		SplunkEnterpriseConfigTraceIndex,
	} {
		config := []core.IntegrationConfigValue{
			{Name: SplunkEnterpriseConfigURL, Value: "https://splunk.example.com:8089"},
			{Name: SplunkEnterpriseConfigAuthType, Value: SplunkEnterpriseAuthToken},
			{Name: SplunkEnterpriseConfigToken, Value: "t"},
			{Name: key, Value: `evil" | delete`},
		}
		errs := SplunkEnterprise{}.ValidateConfig(nil, config, "acct")
		assert.NotEmpty(t, errs, "%s must be validated at the form boundary", key)
	}
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

// Every field ValidateConfig guards with IsSafeSplunkIndexName must be IsTestable.
//
// The dynamic form decides whether to re-validate an edit by asking whether any
// is_testable field changed (shouldSkipValidation in IntegrationDynamicFormModal.jsx).
// When nothing testable changed it sends skip_validation=true, and CreateIntegrationConfig
// then skips ValidateConfig entirely -- so an index or app name edited on its own would be
// persisted without the SPL-safety check, and without the probe's datatype check. These
// names are interpolated into SPL as bare tokens and cannot be parameterized, so that is a
// correctness and an injection hole at once.
//
// Asserting the whole guarded set, rather than the four names, means a fifth
// SPL-interpolated field cannot be added to ValidateConfig without landing here too.
func TestSplunkEnterprise_ConfigSchema_SPLInterpolatedFieldsAreTestable(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	for _, key := range []string{
		SplunkEnterpriseConfigLogIndex,
		SplunkEnterpriseConfigMetricIndex,
		SplunkEnterpriseConfigTraceIndex,
		SplunkEnterpriseConfigApp,
	} {
		assert.True(t, schema.Properties[key].IsTestable,
			"%s is validated by IsSafeSplunkIndexName, so it must be IsTestable or an edit to it skips validation", key)
	}
}

// The connection fields were already testable; the index change must not have displaced them.
func TestSplunkEnterprise_ConfigSchema_ConnectionFieldsStayTestable(t *testing.T) {
	schema := SplunkEnterprise{}.ConfigSchema()
	for _, key := range []string{
		SplunkEnterpriseConfigURL,
		SplunkEnterpriseConfigToken,
		SplunkEnterpriseConfigUsername,
		SplunkEnterpriseConfigPassword,
	} {
		assert.True(t, schema.Properties[key].IsTestable, "%s must stay IsTestable", key)
	}
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
		assert.Contains(t, []string{SplunkEnterpriseServerInfoPath, SplunkEnterpriseIndexesPath}, r.URL.Path)
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

// ----- TestConnection: index check ------------------------------------------

// splunkIndexEntry renders one /services/data/indexes entry.
func splunkIndexEntry(name, datatype string, disabled bool) string {
	return fmt.Sprintf(`{"name":%q,"content":{"datatype":%q,"disabled":%t}}`, name, datatype, disabled)
}

// splunkIndexServer answers server/info with 200 and data/indexes with the given
// status and listing entries.
func splunkIndexServer(t *testing.T, status int, entries ...string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == SplunkEnterpriseServerInfoPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		assert.Equal(t, SplunkEnterpriseIndexesPath, r.URL.Path)
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"entry":[%s]}`, strings.Join(entries, ","))
	}))
	t.Cleanup(server.Close)
	return server
}

// splunkIndexCfg is a valid config pointed at server with the given index fields set.
func splunkIndexCfg(server *httptest.Server, indexes map[string]string) []core.IntegrationConfigValue {
	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigURL] = server.URL
	for k, v := range indexes {
		cfg[k] = v
	}
	return splunkEnterpriseCfg(cfg)
}

// One request covers every configured index, authenticated exactly like server/info,
// and it must ask for metric indexes too: without datatype=all Splunk omits them, and a
// correctly configured metric index would look absent.
func TestSplunkEnterprise_TestConnection_IndexListingRequest(t *testing.T) {
	var listings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer a-token", r.Header.Get("Authorization"))
		if r.URL.Path == SplunkEnterpriseServerInfoPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		listings.Add(1)
		q := r.URL.Query()
		assert.Equal(t, SplunkEnterpriseIndexesPath, r.URL.Path)
		assert.Equal(t, "json", q.Get("output_mode"))
		assert.Equal(t, "all", q.Get("datatype"))
		assert.Equal(t, "0", q.Get("count"))
		assert.Equal(t, "name=otel_logs OR name=otel_metrics OR name=otel_traces", q.Get("search"))
		_, _ = w.Write([]byte(`{"entry":[` + strings.Join([]string{
			splunkIndexEntry("otel_logs", "event", false),
			splunkIndexEntry("otel_metrics", "metric", false),
			splunkIndexEntry("otel_traces", "event", false),
		}, ",") + `]}`))
	}))
	defer server.Close()

	err := SplunkEnterprise{}.TestConnection(nil, splunkIndexCfg(server, map[string]string{
		SplunkEnterpriseConfigLogIndex:    "otel_logs",
		SplunkEnterpriseConfigMetricIndex: "otel_metrics",
		SplunkEnterpriseConfigTraceIndex:  "otel_traces",
	}), "acct")
	assert.NoError(t, err)
	assert.Equal(t, int32(1), listings.Load(), "all indexes are checked in a single request")
}

// A listing that positively names a configured index with the wrong datatype, or as
// disabled, is a definite misconfiguration: every query on that signal would fail. The
// message leads with "configuration:" and names the field, so the form and nubi's
// diagnosis both point at the setting to change.
func TestSplunkEnterprise_TestConnection_IndexContradictionsFail(t *testing.T) {
	cases := []struct {
		name    string
		indexes map[string]string
		entries []string
		want    []string
	}{
		{
			name:    "event index as the metric index",
			indexes: map[string]string{SplunkEnterpriseConfigMetricIndex: "otel_logs"},
			entries: []string{splunkIndexEntry("main", "event", false), splunkIndexEntry("otel_logs", "event", false)},
			want:    []string{SplunkEnterpriseConfigMetricIndex, `"otel_logs"`, "event index", "datatype=metric"},
		},
		{
			name:    "metric index as the log index",
			indexes: map[string]string{SplunkEnterpriseConfigLogIndex: "otel_metrics"},
			entries: []string{splunkIndexEntry("otel_metrics", "metric", false)},
			want:    []string{SplunkEnterpriseConfigLogIndex, `"otel_metrics"`, "metrics index", "logs need an event index"},
		},
		{
			name:    "metric index as the trace index",
			indexes: map[string]string{SplunkEnterpriseConfigTraceIndex: "otel_metrics"},
			entries: []string{splunkIndexEntry("main", "event", false), splunkIndexEntry("otel_metrics", "metric", false)},
			want:    []string{SplunkEnterpriseConfigTraceIndex, `"otel_metrics"`, "traces need an event index"},
		},
		{
			name:    "disabled index",
			indexes: map[string]string{SplunkEnterpriseConfigLogIndex: "otel_logs"},
			entries: []string{splunkIndexEntry("otel_logs", "event", true)},
			want:    []string{SplunkEnterpriseConfigLogIndex, `"otel_logs"`, "disabled"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := splunkIndexServer(t, http.StatusOK, tc.entries...)
			err := SplunkEnterprise{}.TestConnection(nil, splunkIndexCfg(server, tc.indexes), "acct")
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), "configuration: "), "got %q", err.Error())
			for _, want := range tc.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// Everything short of a definite contradiction passes. data/indexes reports only the
// answering node's own indexes, so on a distributed Splunk a working index can be
// absent from it, and a search-only role may get 403 or 404 for it outright. Failing
// on those would reject connections whose searches work.
func TestSplunkEnterprise_TestConnection_IndexCheckInertWhenUnsure(t *testing.T) {
	metricOnly := map[string]string{SplunkEnterpriseConfigMetricIndex: "otel"}
	cases := []struct {
		name    string
		status  int
		indexes map[string]string
		entries []string
	}{
		{"index absent from listing", http.StatusOK, map[string]string{SplunkEnterpriseConfigMetricIndex: "nosuch"}, nil},
		{
			// search=name=otel matches substrings, so Splunk returns otel_logs for it;
			// that is a different index and says nothing about "otel".
			"only a longer sibling name, opposite datatype", http.StatusOK, metricOnly,
			[]string{splunkIndexEntry("otel_logs", "event", false)},
		},
		{"listing forbidden", http.StatusForbidden, metricOnly, nil},
		{"listing not found", http.StatusNotFound, metricOnly, nil},
		{"datatype not reported", http.StatusOK, metricOnly, []string{splunkIndexEntry("otel", "", false)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := splunkIndexServer(t, tc.status, tc.entries...)
			err := SplunkEnterprise{}.TestConnection(nil, splunkIndexCfg(server, tc.indexes), "acct")
			assert.NoError(t, err)
		})
	}
}

func TestSplunkEnterprise_TestConnection_IndexCheckUndecodableListingPasses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == SplunkEnterpriseIndexesPath {
			_, _ = w.Write([]byte(`<html>not json</html>`))
		}
	}))
	defer server.Close()

	err := SplunkEnterprise{}.TestConnection(nil, splunkIndexCfg(server, nil), "acct")
	assert.NoError(t, err)
}

// The listing has its own fixed timeout. A slow listing passes rather than failing the
// test, and it cannot stretch the probe past that budget.
func TestSplunkEnterprise_TestConnection_IndexCheckTimeoutPassesAndIsBounded(t *testing.T) {
	previous := splunkEnterpriseIndexCheckTimeout
	splunkEnterpriseIndexCheckTimeout = 200 * time.Millisecond
	t.Cleanup(func() { splunkEnterpriseIndexCheckTimeout = previous })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == SplunkEnterpriseIndexesPath {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	start := time.Now()
	err := SplunkEnterprise{}.TestConnection(nil, splunkIndexCfg(server, map[string]string{
		SplunkEnterpriseConfigMetricIndex: "otel_logs",
	}), "acct")
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

// ----- TestConnection: missing-port hint ------------------------------------

func TestSplunkEnterpriseMissingPortHint(t *testing.T) {
	refused := errors.New(`Get "https://splunk.example.com/services/server/info": dial tcp 10.0.0.1:443: connect: connection refused`)
	clientTimeout := errors.New(`Get "https://splunk.example.com/services/server/info": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	ioTimeout := errors.New(`Get "https://splunk.example.com/services/server/info": dial tcp 10.0.0.1:443: i/o timeout`)
	eof := fmt.Errorf(`Get "https://splunk.example.com/services/server/info": %w`, io.EOF)

	t.Run("https refused names 443 and suggests 8089", func(t *testing.T) {
		hint := splunkEnterpriseMissingPortHint("https://splunk.example.com", refused)
		assert.Contains(t, hint, "port 443 was used")
		assert.Contains(t, hint, "https://splunk.example.com:8089")
	})
	t.Run("http refused names 80", func(t *testing.T) {
		hint := splunkEnterpriseMissingPortHint("http://splunk.example.com", refused)
		assert.Contains(t, hint, "port 80 was used")
		assert.Contains(t, hint, "http://splunk.example.com:8089")
	})
	t.Run("timeouts get the hint", func(t *testing.T) {
		assert.Contains(t, splunkEnterpriseMissingPortHint("https://splunk.example.com", clientTimeout), "8089")
		assert.Contains(t, splunkEnterpriseMissingPortHint("https://splunk.example.com", ioTimeout), "8089")
	})
	// EOF means something did answer on the default port (typically a proxy or the
	// web UI), so the port is not the likely mistake.
	t.Run("EOF gets no hint", func(t *testing.T) {
		assert.Empty(t, splunkEnterpriseMissingPortHint("https://splunk.example.com", eof))
	})
	t.Run("explicit port gets no hint", func(t *testing.T) {
		assert.Empty(t, splunkEnterpriseMissingPortHint("https://splunk.example.com:8000", refused))
		assert.Empty(t, splunkEnterpriseMissingPortHint("https://splunk.example.com:8089", clientTimeout))
	})
}

// A refused connection on an explicit port keeps today's message: the port is there,
// so the hint would be wrong.
func TestSplunkEnterprise_TestConnection_RefusedWithPortHasNoHint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	cfg := validSplunkEnterpriseCfg()
	cfg[SplunkEnterpriseConfigURL] = "http://" + addr

	err = SplunkEnterprise{}.TestConnection(nil, splunkEnterpriseCfg(cfg), "acct")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the server may be down")
	assert.NotContains(t, err.Error(), "has no port")
}
