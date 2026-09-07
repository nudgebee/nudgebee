package integrations

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promCfg builds a config slice from a convenience map.
func promCfg(m map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(m))
	for k, v := range m {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

// ----- metadata / registration ---------------------------------------------

func TestPrometheus_Name(t *testing.T) {
	assert.Equal(t, "prometheus", Prometheus{}.Name())
}

func TestPrometheus_Category(t *testing.T) {
	assert.Equal(t, core.IntegrationCategoryMetrics, Prometheus{}.Category())
}

// The user-source registration must not displace the agent one: both live under
// the same integration type name and an account can have a row of each.
func TestPrometheus_RegistrationIsSourceQualified(t *testing.T) {
	userIntegration, found := core.GetIntegrationBySource("prometheus", "user")
	require.True(t, found)
	assert.IsType(t, Prometheus{}, userIntegration)

	agentIntegration, found := core.GetIntegration("prometheus")
	require.True(t, found)
	assert.IsType(t, PrometheusAgent{}, agentIntegration,
		"plain lookup must still resolve to the agent integration")
}

// ----- schema ---------------------------------------------------------------

func TestPrometheus_ConfigSchema_RequiredAndTestable(t *testing.T) {
	schema := Prometheus{}.ConfigSchema()
	assert.True(t, schema.Testable, "schema must declare Testable=true")
	assert.Contains(t, schema.Required, PrometheusURLKey)
	assert.Contains(t, schema.Required, PrometheusAuthTypeKey)
}

// Every field the probe actually sends must be testable. The dynamic form uses
// is_testable to decide whether an edit needs re-validation: if a field the
// connection depends on is not marked, changing only that field makes the form
// send skip_validation=true and the backend never runs ValidateConfig, so a
// malformed value saves. Extra headers are sent alongside auth on every probe.
func TestPrometheus_ConfigSchema_ConnectionFieldsAreTestable(t *testing.T) {
	props := Prometheus{}.ConfigSchema().Properties

	nonConnection := map[string]bool{
		core.IntegrationConfigName:  true,
		core.AccountId:              true,
		core.DefaultMetricsProvider: true,
	}

	for name, prop := range props {
		if nonConnection[name] {
			continue
		}
		assert.True(t, prop.IsTestable, "%s is sent on the probe, so it must be is_testable", name)
	}

	assert.True(t, props[PrometheusExtraHeadersKey].IsTestable,
		"extra headers must be testable or a headers-only edit skips validation")
}

// The dynamic form skips any property without a description, which would make
// the field invisible in the UI while still being required by the backend.
func TestPrometheus_ConfigSchema_EveryPropertyHasDescription(t *testing.T) {
	for name, prop := range (Prometheus{}).ConfigSchema().Properties {
		assert.NotEmpty(t, strings.TrimSpace(prop.Description),
			"property %q needs a description or the form will not render it", name)
	}
}

func TestPrometheus_ConfigSchema_SecretsAreEncrypted(t *testing.T) {
	props := Prometheus{}.ConfigSchema().Properties
	for _, key := range []string{
		PrometheusPasswordKey,
		PrometheusBearerTokenKey,
		PrometheusAWSSecretAccessKeyKey,
		PrometheusCoralogixTokenKey,
		PrometheusAzureClientSecretKey,
		PrometheusExtraHeadersKey,
	} {
		assert.True(t, props[key].IsEncrypted, "%s must be stored encrypted", key)
	}
	for _, key := range []string{PrometheusURLKey, PrometheusUsernameKey, PrometheusAWSAccessKeyIDKey, PrometheusAWSRegionKey} {
		assert.False(t, props[key].IsEncrypted, "%s is not a secret", key)
	}
}

func TestPrometheus_ConfigSchema_AuthTypeEnumCoversAgentSchemes(t *testing.T) {
	authType := Prometheus{}.ConfigSchema().Properties[PrometheusAuthTypeKey]
	assert.Equal(t, PrometheusAuthNone, authType.Default)
	assert.ElementsMatch(t, []any{
		PrometheusAuthNone, PrometheusAuthBasic, PrometheusAuthBearerToken,
		PrometheusAuthAWSSigV4, PrometheusAuthCoralogix, PrometheusAuthAzureAD,
	}, authType.Enum)
}

// Each credential field must be shown and required only under its own auth_type.
func TestPrometheus_ConfigSchema_ConditionalFieldsMatchAuthType(t *testing.T) {
	props := Prometheus{}.ConfigSchema().Properties
	cases := map[string]string{
		PrometheusUsernameKey:           PrometheusAuthBasic,
		PrometheusPasswordKey:           PrometheusAuthBasic,
		PrometheusBearerTokenKey:        PrometheusAuthBearerToken,
		PrometheusAWSAccessKeyIDKey:     PrometheusAuthAWSSigV4,
		PrometheusAWSSecretAccessKeyKey: PrometheusAuthAWSSigV4,
		PrometheusAWSRegionKey:          PrometheusAuthAWSSigV4,
		PrometheusCoralogixTokenKey:     PrometheusAuthCoralogix,
		PrometheusAzureClientIDKey:      PrometheusAuthAzureAD,
		PrometheusAzureClientSecretKey:  PrometheusAuthAzureAD,
		PrometheusAzureTenantIDKey:      PrometheusAuthAzureAD,
	}
	for field, authType := range cases {
		assert.Equal(t, map[string]any{PrometheusAuthTypeKey: authType}, props[field].ShowWhen, "ShowWhen for %s", field)
		assert.Equal(t, map[string]any{PrometheusAuthTypeKey: authType}, props[field].RequiredWhen, "RequiredWhen for %s", field)
	}

	// Extra headers ride along with every scheme, so they must never be gated.
	assert.Nil(t, props[PrometheusExtraHeadersKey].ShowWhen)
	assert.Nil(t, props[PrometheusExtraHeadersKey].RequiredWhen)
	assert.True(t, props[PrometheusExtraHeadersKey].Multiline)
}

func TestPrometheus_ConfigSchema_Defaults(t *testing.T) {
	props := Prometheus{}.ConfigSchema().Properties
	assert.Equal(t, PrometheusDefaultAWSService, props[PrometheusAWSServiceNameKey].Default)
	assert.Equal(t, PrometheusDefaultAzureResource, props[PrometheusAzureResourceKey].Default)
}

// ----- structural validation ------------------------------------------------

func TestPrometheus_ValidateConfig_URLErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{PrometheusAuthTypeKey: PrometheusAuthNone}), "acc")
		require.NotEmpty(t, errs)
		assert.Contains(t, errs[0].Error(), PrometheusURLKey)
	})

	t.Run("not a url", func(t *testing.T) {
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
			PrometheusURLKey:      "prometheus.example.com",
			PrometheusAuthTypeKey: PrometheusAuthNone,
		}), "acc")
		require.NotEmpty(t, errs)
	})
}

// Unlike SigNoz and Chronosphere, a path is legitimate here: Amazon Managed
// Prometheus serves the API under /workspaces/<id>. Stripping or rejecting it
// would break every AMP workspace.
func TestPrometheus_ValidateConfig_AcceptsURLWithPath(t *testing.T) {
	server := newPrometheusProbeServer(t, func(http.ResponseWriter, *http.Request) {})
	defer server.Close()

	errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
		PrometheusURLKey:      server.URL + "/workspaces/ws-123",
		PrometheusAuthTypeKey: PrometheusAuthNone,
	}), "acc")
	assert.Empty(t, errs)
}

func TestPrometheus_ValidateConfig_MissingCredentialsPerAuthType(t *testing.T) {
	cases := map[string][]string{
		PrometheusAuthBasic:       {PrometheusUsernameKey, PrometheusPasswordKey},
		PrometheusAuthBearerToken: {PrometheusBearerTokenKey},
		PrometheusAuthAWSSigV4:    {PrometheusAWSAccessKeyIDKey, PrometheusAWSSecretAccessKeyKey, PrometheusAWSRegionKey},
		PrometheusAuthCoralogix:   {PrometheusCoralogixTokenKey},
		PrometheusAuthAzureAD:     {PrometheusAzureClientIDKey, PrometheusAzureClientSecretKey, PrometheusAzureTenantIDKey},
	}
	for authType, wantFields := range cases {
		t.Run(authType, func(t *testing.T) {
			errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
				PrometheusURLKey:      "https://prometheus.example.com",
				PrometheusAuthTypeKey: authType,
			}), "acc")
			require.NotEmpty(t, errs)
			joined := errorsToString(errs)
			for _, field := range wantFields {
				assert.Contains(t, joined, field)
			}
		})
	}
}

func TestPrometheus_ValidateConfig_UnknownAuthType(t *testing.T) {
	errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
		PrometheusURLKey:      "https://prometheus.example.com",
		PrometheusAuthTypeKey: "kerberos",
	}), "acc")
	require.NotEmpty(t, errs)
	assert.Contains(t, errorsToString(errs), "kerberos")
}

func TestPrometheus_ValidateConfig_MalformedExtraHeaders(t *testing.T) {
	errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
		PrometheusURLKey:          "https://prometheus.example.com",
		PrometheusAuthTypeKey:     PrometheusAuthNone,
		PrometheusExtraHeadersKey: "oops",
	}), "acc")
	require.NotEmpty(t, errs)
	assert.Contains(t, errorsToString(errs), PrometheusExtraHeadersKey)
}

// ----- live probe + auth ----------------------------------------------------

// newPrometheusProbeServer serves the label-values endpoint the probe calls and
// hands each received request to inspect.
func newPrometheusProbeServer(t *testing.T, inspect func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inspect(w, r)
		if r.Header.Get("X-Test-Status") != "" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["up"]}`))
	}))
}

func TestPrometheus_ValidateConfig_ProbeSendsAuth(t *testing.T) {
	cases := []struct {
		name   string
		config map[string]string
		verify func(t *testing.T, r *http.Request)
	}{
		{
			name: "basic",
			config: map[string]string{
				PrometheusAuthTypeKey: PrometheusAuthBasic,
				PrometheusUsernameKey: "alice",
				PrometheusPasswordKey: "s3cret",
			},
			verify: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("alice:s3cret")),
					r.Header.Get("Authorization"))
			},
		},
		{
			name: "bearer token",
			config: map[string]string{
				PrometheusAuthTypeKey:    PrometheusAuthBearerToken,
				PrometheusBearerTokenKey: "tok-123",
			},
			verify: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "Bearer tok-123", r.Header.Get("Authorization"))
			},
		},
		{
			name: "coralogix uses a bare token header",
			config: map[string]string{
				PrometheusAuthTypeKey:       PrometheusAuthCoralogix,
				PrometheusCoralogixTokenKey: "cx-token",
			},
			verify: func(t *testing.T, r *http.Request) {
				assert.Equal(t, "cx-token", r.Header.Get("token"))
				assert.Empty(t, r.Header.Get("Authorization"))
			},
		},
		{
			name: "aws sigv4",
			config: map[string]string{
				PrometheusAuthTypeKey:           PrometheusAuthAWSSigV4,
				PrometheusAWSAccessKeyIDKey:     "AKIAEXAMPLE",
				PrometheusAWSSecretAccessKeyKey: "secret",
				PrometheusAWSRegionKey:          "us-east-1",
			},
			verify: func(t *testing.T, r *http.Request) {
				auth := r.Header.Get("Authorization")
				assert.Regexp(t,
					regexp.MustCompile(`^AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/\d{8}/us-east-1/aps/aws4_request`),
					auth)
				assert.NotEmpty(t, r.Header.Get("X-Amz-Date"))
			},
		},
		{
			name: "no auth",
			config: map[string]string{
				PrometheusAuthTypeKey: PrometheusAuthNone,
			},
			verify: func(t *testing.T, r *http.Request) {
				assert.Empty(t, r.Header.Get("Authorization"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *http.Request
			server := newPrometheusProbeServer(t, func(_ http.ResponseWriter, r *http.Request) { got = r.Clone(r.Context()) })
			defer server.Close()

			config := map[string]string{PrometheusURLKey: server.URL}
			for k, v := range tc.config {
				config[k] = v
			}

			errs := Prometheus{}.ValidateConfig(nil, promCfg(config), "acc")
			require.Empty(t, errs)
			require.NotNil(t, got)
			assert.Equal(t, "/api/v1/label/__name__/values", got.URL.Path)
			assert.Equal(t, "1", got.URL.Query().Get("limit"))
			tc.verify(t, got)
		})
	}
}

// Extra headers are additive, not an alternative: a Mimir tenant needs
// X-Scope-OrgID alongside whatever authenticates the call.
func TestPrometheus_ValidateConfig_ExtraHeadersApplyAlongsideAuth(t *testing.T) {
	var got *http.Request
	server := newPrometheusProbeServer(t, func(_ http.ResponseWriter, r *http.Request) { got = r.Clone(r.Context()) })
	defer server.Close()

	errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
		PrometheusURLKey:          server.URL,
		PrometheusAuthTypeKey:     PrometheusAuthBasic,
		PrometheusUsernameKey:     "alice",
		PrometheusPasswordKey:     "s3cret",
		PrometheusExtraHeadersKey: "X-Scope-OrgID: tenant-1",
	}), "acc")

	require.Empty(t, errs)
	require.NotNil(t, got)
	assert.Equal(t, "tenant-1", got.Header.Get("X-Scope-OrgID"))
	assert.NotEmpty(t, got.Header.Get("Authorization"))
}

func TestPrometheus_ValidateConfig_ProbeStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusUnauthorized, "401"},
		{http.StatusForbidden, "403"},
		{http.StatusNotFound, "not found"},
		{http.StatusInternalServerError, "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
				PrometheusURLKey:      server.URL,
				PrometheusAuthTypeKey: PrometheusAuthNone,
			}), "acc")
			require.Len(t, errs, 1)
			assert.Contains(t, strings.ToLower(errs[0].Error()), strings.ToLower(tc.want))
		})
	}
}

func TestPrometheus_ValidateConfig_UnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close() // nothing is listening now

	errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
		PrometheusURLKey:      url,
		PrometheusAuthTypeKey: PrometheusAuthNone,
	}), "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "failed to connect")
}

// ----- config resolution ----------------------------------------------------

// An account can hold both an agent-created row (no URL, no credentials) and a
// user-configured one. Picking the wrong one produces an empty-URL failure that
// looks like a missing integration.
func TestPickPrometheusUserIntegration_SkipsAgentRow(t *testing.T) {
	agentRow := core.IntegrationDto{Id: "agent-row", Source: "agent"}
	userRow := core.IntegrationDto{Id: "user-row", Source: "user"}

	got, found := pickPrometheusUserIntegration([]core.IntegrationDto{agentRow, userRow})
	require.True(t, found)
	assert.Equal(t, "user-row", got.Id)

	_, found = pickPrometheusUserIntegration([]core.IntegrationDto{agentRow})
	assert.False(t, found, "an agent-only account has no direct connection to use")

	_, found = pickPrometheusUserIntegration(nil)
	assert.False(t, found)
}

// ----- helpers --------------------------------------------------------------

func errorsToString(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}

// ----- ruler --------------------------------------------------------------

func TestPrometheus_ConfigSchema_RulerFields(t *testing.T) {
	props := Prometheus{}.ConfigSchema().Properties
	rulerType := props[PrometheusRulerTypeKey]
	assert.Equal(t, PrometheusRulerNone, rulerType.Default, "a ruler is opt-in")
	assert.ElementsMatch(t, []any{PrometheusRulerNone, PrometheusRulerMimirCortex}, rulerType.Enum)
	assert.Nil(t, rulerType.ShowWhen, "the choice applies under every auth scheme")
	// The URL only matters once a ruler is declared, and is never required: it
	// defaults to the query endpoint.
	assert.Equal(t, map[string]any{PrometheusRulerTypeKey: PrometheusRulerMimirCortex}, props[PrometheusRulerURLKey].ShowWhen)
	assert.Nil(t, props[PrometheusRulerURLKey].RequiredWhen)
}

// newPrometheusWithRulerServer serves the label-values probe on the query path
// and answers the ruler listing as told, so save-time validation can be driven
// end to end against both.
func newPrometheusWithRulerServer(t *testing.T, rulerStatus int, rulerBody string, sawRuler *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/config/v1/rules") {
			*sawRuler = true
			w.WriteHeader(rulerStatus)
			_, _ = w.Write([]byte(rulerBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":["up"]}`))
	}))
}

func TestPrometheus_ValidateConfig_RulerProbe(t *testing.T) {
	t.Run("no ruler declared makes no ruler request", func(t *testing.T) {
		var sawRuler bool
		server := newPrometheusWithRulerServer(t, http.StatusOK, "", &sawRuler)
		defer server.Close()
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{PrometheusURLKey: server.URL}), "acc")
		require.Empty(t, errs)
		assert.False(t, sawRuler)
	})

	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string // empty = pass
	}{
		{name: "groups listed", status: http.StatusOK, body: "nudgebee:\n- name: x\n"},
		{name: "empty tenant on Cortex/Mimir is a working ruler", status: http.StatusNotFound, body: "no rule groups found\n"},
		{name: "bare 404 is a plain Prometheus", status: http.StatusNotFound, body: "404 page not found\n", wantErr: "no ruler API"},
		{name: "405 is a plain Prometheus", status: http.StatusMethodNotAllowed, body: "", wantErr: "no ruler API"},
		{name: "victoriametrics 400 names the unsupported path", status: http.StatusBadRequest, body: `requestURI: /config/v1/rules; unsupported path requested: "/config/v1/rules"`, wantErr: "no ruler API"},
		{name: "an unrelated 400 is not reported as a missing ruler", status: http.StatusBadRequest, body: "bad query parameter", wantErr: "unexpected status"},
		{name: "401 names the credentials", status: http.StatusUnauthorized, body: "", wantErr: "rejected the credentials"},
		{name: "403 names the scope", status: http.StatusForbidden, body: "", wantErr: "rules:write"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sawRuler bool
			server := newPrometheusWithRulerServer(t, tc.status, tc.body, &sawRuler)
			defer server.Close()
			errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
				PrometheusURLKey:       server.URL,
				PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
			}), "acc")
			assert.True(t, sawRuler, "a declared ruler must be probed on save")
			if tc.wantErr == "" {
				assert.Empty(t, errs, errorsToString(errs))
				return
			}
			require.NotEmpty(t, errs)
			assert.Contains(t, errorsToString(errs), tc.wantErr)
		})
	}

	t.Run("separate ruler URL is the one probed", func(t *testing.T) {
		var sawQueryRuler, sawRuler bool
		query := newPrometheusWithRulerServer(t, http.StatusNotFound, "404 page not found", &sawQueryRuler)
		defer query.Close()
		ruler := newPrometheusWithRulerServer(t, http.StatusOK, "", &sawRuler)
		defer ruler.Close()
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
			PrometheusURLKey:       query.URL,
			PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
			PrometheusRulerURLKey:  ruler.URL + "/prometheus",
		}), "acc")
		assert.Empty(t, errs, errorsToString(errs))
		assert.True(t, sawRuler)
		assert.False(t, sawQueryRuler)
	})

	t.Run("ruler URL is checked like the query URL", func(t *testing.T) {
		errs := Prometheus{}.ValidateConfig(nil, promCfg(map[string]string{
			PrometheusURLKey:       "https://prometheus.example.com",
			PrometheusRulerTypeKey: PrometheusRulerMimirCortex,
			PrometheusRulerURLKey:  "not a url",
		}), "acc")
		require.NotEmpty(t, errs)
		assert.Contains(t, errorsToString(errs), PrometheusRulerURLKey)
	})
}
