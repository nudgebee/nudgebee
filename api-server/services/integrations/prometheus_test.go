package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// resetAzureTokenCache clears cached tokens so cases don't leak into each other.
func resetAzureTokenCache(t *testing.T) {
	t.Helper()
	azureTokenMu.Lock()
	azureTokenCache = map[string]azureTokenEntry{}
	azureTokenMu.Unlock()
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

// ----- header parsing -------------------------------------------------------

func TestParsePrometheusHeaders(t *testing.T) {
	t.Run("multiline", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-Scope-OrgID: tenant-1\nX-Extra: value")
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// The agent takes this form from PROMETHEUS_HEADERS, so a config copied
	// across from an agent install must parse.
	t.Run("comma separated", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-Scope-OrgID: tenant-1, X-Extra: value")
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// A value may legitimately contain a comma, so a line that already parses
	// as one header is never re-split.
	t.Run("value containing comma is kept whole", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("X-List: a, b, c")
		require.NoError(t, err)
		assert.Equal(t, "a, b, c", headers.Get("X-List"))
	})

	t.Run("blank input", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("  \n\n ")
		require.NoError(t, err)
		assert.Nil(t, headers)
	})

	t.Run("malformed line", func(t *testing.T) {
		_, err := ParsePrometheusHeaders("not-a-header")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected")
	})

	// The JSON form matches the shape llm_extra_headers already uses, so an
	// operator who learned one field does not have to learn a second syntax.
	t.Run("json object", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": "tenant-1", "X-Extra": "value"}`)
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
		assert.Equal(t, "value", headers.Get("X-Extra"))
	})

	// A repeated header is what the line form gets from writing the name twice.
	t.Run("json array value repeats the header", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Repeat": ["a", "b"]}`)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, headers.Values("X-Repeat"))
	})

	t.Run("json values are trimmed", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": "  tenant-1  "}`)
		require.NoError(t, err)
		assert.Equal(t, "tenant-1", headers.Get("X-Scope-OrgID"))
	})

	t.Run("empty json object", func(t *testing.T) {
		headers, err := ParsePrometheusHeaders("{}")
		require.NoError(t, err)
		assert.Nil(t, headers)
	})

	// A leading brace commits to JSON: the error must name the real problem
	// rather than the line parser's "expected \"Header: value\"".
	t.Run("malformed json is not retried as lines", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Scope-OrgID": }`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid JSON header object")
	})

	t.Run("json non-string value", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Count": 3}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a string or an array of strings")
	})

	t.Run("json array with non-string item", func(t *testing.T) {
		_, err := ParsePrometheusHeaders(`{"X-Repeat": ["a", 2]}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "array values must be strings")
	})
}

func TestParsePrometheusAdditionalLabels(t *testing.T) {
	t.Run("single label", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"cluster": "prod"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="prod"`, got)
	})

	// Go map order is randomised, so the generated fragment must be sorted or the
	// same config would produce a different query on every call.
	t.Run("multiple labels are sorted", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"region": "us", "cluster": "prod"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="prod",region="us"`, got)
	})

	t.Run("empty", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels("  ")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// A configured value lands inside every query the builders produce, so a
	// quote must not be able to close the matcher and append arbitrary PromQL.
	t.Run("value quotes are escaped", func(t *testing.T) {
		got, err := ParsePrometheusAdditionalLabels(`{"cluster": "pr\"od"}`)
		require.NoError(t, err)
		assert.Equal(t, `cluster="pr\"od"`, got)
	})

	t.Run("rejects invalid label name", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`{"cluster\"} or up{": "x"}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid label name")
	})

	t.Run("rejects non-object", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`cluster="prod"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a JSON object")
	})

	t.Run("rejects non-string value", func(t *testing.T) {
		_, err := ParsePrometheusAdditionalLabels(`{"cluster": 3}`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be a JSON object")
	})
}

func TestExpandClusterPlaceholder(t *testing.T) {
	// The templates write __CLUSTER__ with no comma after it, so the expansion
	// supplies the separator -- matching the relay-server byte for byte.
	t.Run("configured", func(t *testing.T) {
		got := ExpandClusterPlaceholder(`node_cpu_seconds_total{__CLUSTER__ mode!="idle"}`, `cluster="prod"`)
		// Two spaces before `mode`: one from the " , " separator, one already in the
		// template. The relay-server produces exactly the same string, and PromQL
		// ignores the whitespace -- asserted verbatim to keep the paths identical.
		assert.Equal(t, `node_cpu_seconds_total{cluster="prod" ,  mode!="idle"}`, got)
	})

	// The unconfigured case is the actual bug fix: the token must not survive
	// into the request, or the backend rejects the query outright.
	t.Run("unconfigured drops the token", func(t *testing.T) {
		got := ExpandClusterPlaceholder(`node_cpu_seconds_total{__CLUSTER__ mode!="idle"}`, "")
		assert.Equal(t, `node_cpu_seconds_total{ mode!="idle"}`, got)
		assert.NotContains(t, got, ClusterPlaceholder)
	})

	t.Run("bare selector unconfigured", func(t *testing.T) {
		assert.Equal(t, `kube_node_info{}`, ExpandClusterPlaceholder(`kube_node_info{__CLUSTER__}`, ""))
	})

	// An `or`-joined query carries the token once per selector; injectPromQLMatchers
	// only rewrites the first, so this replacement has to cover them all.
	t.Run("every occurrence is replaced", func(t *testing.T) {
		in := `sum(a{__CLUSTER__ x="1"}) or sum(b{__CLUSTER__ y="2"})`
		got := ExpandClusterPlaceholder(in, `cluster="prod"`)
		assert.NotContains(t, got, ClusterPlaceholder)
		assert.Equal(t, 2, strings.Count(got, `cluster="prod"`))
	})
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

// ----- azure ad -------------------------------------------------------------

func TestPrometheus_AzureAD_MintsAndCachesToken(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	var tokenRequests int
	var lastForm neturl.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		require.NoError(t, r.ParseForm())
		lastForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"azure-token","expires_in":"3600"}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	var got *http.Request
	promServer := newPrometheusProbeServer(t, func(_ http.ResponseWriter, r *http.Request) { got = r.Clone(r.Context()) })
	defer promServer.Close()

	config := promCfg(map[string]string{
		PrometheusURLKey:               promServer.URL,
		PrometheusAuthTypeKey:          PrometheusAuthAzureAD,
		PrometheusAzureClientIDKey:     "client-id",
		PrometheusAzureClientSecretKey: "client-secret",
		PrometheusAzureTenantIDKey:     "tenant-id",
	})

	require.Empty(t, Prometheus{}.ValidateConfig(nil, config, "acc"))
	require.NotNil(t, got)
	assert.Equal(t, "Bearer azure-token", got.Header.Get("Authorization"))
	assert.Equal(t, "client_credentials", lastForm.Get("grant_type"))
	assert.Equal(t, "client-id", lastForm.Get("client_id"))
	assert.Equal(t, "client-secret", lastForm.Get("client_secret"))
	assert.Equal(t, PrometheusDefaultAzureResource, lastForm.Get("resource"))
	assert.Equal(t, 1, tokenRequests)

	// A second call reuses the cached token rather than minting another.
	require.Empty(t, Prometheus{}.ValidateConfig(nil, config, "acc"))
	assert.Equal(t, 1, tokenRequests, "token should be served from cache")
}

func TestPrometheus_AzureAD_RemintsExpiredToken(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	var tokenRequests int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		// expires_in below the 60s refresh margin, so the token is never reusable.
		_, _ = w.Write([]byte(`{"access_token":"short-lived","expires_in":30}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	promServer := newPrometheusProbeServer(t, func(http.ResponseWriter, *http.Request) {})
	defer promServer.Close()

	config := promCfg(map[string]string{
		PrometheusURLKey:               promServer.URL,
		PrometheusAuthTypeKey:          PrometheusAuthAzureAD,
		PrometheusAzureClientIDKey:     "client-id",
		PrometheusAzureClientSecretKey: "client-secret",
		PrometheusAzureTenantIDKey:     "tenant-id",
	})

	require.Empty(t, Prometheus{}.ValidateConfig(nil, config, "acc"))
	require.Empty(t, Prometheus{}.ValidateConfig(nil, config, "acc"))
	assert.Equal(t, 2, tokenRequests, "an expiring token must be re-minted")
}

// azureTokenMu is process-wide, so holding it across the Azure AD round trip would
// serialise token minting across every tenant — one slow response stalling all of
// them. Distinct tenants are used deliberately: they occupy different cache keys
// and share nothing but the mutex, so if they cannot mint concurrently the lock is
// being held across the network call again.
func TestPrometheus_AzureAD_MintsConcurrentlyAcrossTenants(t *testing.T) {
	resetAzureTokenCache(t)
	t.Cleanup(func() { resetAzureTokenCache(t) })

	const callers = 4

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	allArrived := make(chan struct{})
	var once sync.Once

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		reached := inFlight == callers
		mu.Unlock()

		// Hold every request open until all of them have arrived. If the mutex were
		// still held across the mint, only one could ever be in flight and this
		// would time out instead of releasing.
		if reached {
			once.Do(func() { close(allArrived) })
		}
		select {
		case <-allArrived:
		case <-time.After(5 * time.Second):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"azure-token","expires_in":"3600"}`))
	}))
	defer tokenServer.Close()

	original := azureTokenEndpointBase
	azureTokenEndpointBase = tokenServer.URL
	t.Cleanup(func() { azureTokenEndpointBase = original })

	var wg sync.WaitGroup
	errs := make([]error, callers)
	tokens := make([]string, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg := PrometheusUserConfig{
				AzureTenantID:     fmt.Sprintf("tenant-%d", i),
				AzureClientID:     "client-id",
				AzureClientSecret: "client-secret",
				AzureResource:     PrometheusDefaultAzureResource,
			}
			tokens[i], errs[i] = cfg.azureBearer(context.Background())
		}(i)
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		require.NoError(t, errs[i])
		assert.Equal(t, "azure-token", tokens[i])
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, callers, maxInFlight, "tenants must mint concurrently; the cache lock must not be held across the token request")
}

func TestAzureTokenExpiry(t *testing.T) {
	t.Run("prefers absolute expires_on", func(t *testing.T) {
		want := time.Now().Add(2 * time.Hour).Unix()
		got := azureTokenExpiry(json.Number(strconv.FormatInt(want, 10)), json.Number("60"))
		assert.Equal(t, want, got.Unix())
	})
	t.Run("falls back to expires_in", func(t *testing.T) {
		got := azureTokenExpiry(json.Number("0"), json.Number("600"))
		assert.WithinDuration(t, time.Now().Add(600*time.Second), got, time.Minute)
	})
	t.Run("defaults when neither parses", func(t *testing.T) {
		got := azureTokenExpiry("", "")
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), got, time.Minute)
	})
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

func TestNewPrometheusUserConfig_Defaults(t *testing.T) {
	cfg, err := NewPrometheusUserConfig(map[string]string{
		PrometheusURLKey: "https://prometheus.example.com/prometheus/",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://prometheus.example.com/prometheus", cfg.URL, "trailing slash trimmed, path kept")
	assert.Equal(t, PrometheusAuthNone, cfg.AuthType)
	assert.Equal(t, PrometheusDefaultAWSService, cfg.AWSService)
	assert.Equal(t, PrometheusDefaultAzureResource, cfg.AzureResource)
}

// ----- helpers --------------------------------------------------------------

func errorsToString(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}
