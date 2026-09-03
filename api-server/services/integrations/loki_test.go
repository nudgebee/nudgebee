package integrations

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lokiValues(pairs map[string]string) []core.IntegrationConfigValue {
	values := make([]core.IntegrationConfigValue, 0, len(pairs))
	for name, value := range pairs {
		values = append(values, core.IntegrationConfigValue{Name: name, Value: value})
	}
	return values
}

func TestLokiConfigSchema(t *testing.T) {
	schema := Loki{}.ConfigSchema()

	assert.Equal(t, []string{"loki_url"}, schema.Required)
	assert.True(t, schema.Testable, "the user-mode integration must offer a Test Connection")

	// Every field that can carry a credential must be stored encrypted.
	assert.True(t, schema.Properties["loki_password"].IsEncrypted)
	assert.True(t, schema.Properties["loki_bearer_token"].IsEncrypted)
	assert.True(t, schema.Properties["loki_headers"].IsEncrypted)
	assert.False(t, schema.Properties["loki_url"].IsEncrypted)

	// The auth selector drives which credential fields the form shows, so each one
	// must be gated on it — otherwise the form goes back to showing every method at once.
	authType := schema.Properties["loki_auth_type"]
	assert.Equal(t, LokiAuthNone, authType.Default)
	assert.Equal(t, []any{LokiAuthNone, LokiAuthBasic, LokiAuthBearer}, authType.Enum)
	for _, field := range []string{"loki_username", "loki_password"} {
		assert.Equal(t, map[string]any{"loki_auth_type": LokiAuthBasic}, schema.Properties[field].ShowWhen, field)
		assert.Equal(t, map[string]any{"loki_auth_type": LokiAuthBasic}, schema.Properties[field].RequiredWhen, field)
	}
	assert.Equal(t, map[string]any{"loki_auth_type": LokiAuthBearer}, schema.Properties["loki_bearer_token"].ShowWhen)

	// Tenant ID is multi-tenancy, not authentication — it stays visible for every method.
	assert.Nil(t, schema.Properties["loki_tenant_id"].ShowWhen)

	assert.Equal(t, "listAccounts", schema.Properties[core.AccountId].AutoGenerateFunc)
	assert.Contains(t, schema.Properties, core.DefaultLogProvider)
	assert.Equal(t, core.IntegrationCategoryLog, Loki{}.Category())
	assert.Equal(t, "loki", Loki{}.Name())
}

// The user-mode descriptor must be reachable by (name, source) without displacing the
// agent one, which still owns the plain "loki" key.
func TestLokiRegistrationCoexistsWithAgent(t *testing.T) {
	userIntegration, ok := core.GetIntegrationBySource("loki", "user")
	require.True(t, ok)
	assert.IsType(t, Loki{}, userIntegration)

	agentIntegration, ok := core.GetIntegrationBySource("loki", "agent")
	require.True(t, ok)
	assert.IsType(t, LokiAgent{}, agentIntegration)
}

func TestLokiValidateConfig(t *testing.T) {
	testCases := []struct {
		name    string
		config  map[string]string
		wantErr string
	}{
		{
			name:   "minimal valid config",
			config: map[string]string{"loki_url": "http://loki.example.com"},
		},
		{
			name:   "valid with basic auth and tenant",
			config: map[string]string{"loki_url": "https://loki.example.com", "loki_auth_type": LokiAuthBasic, "loki_username": "1234", "loki_password": "token", "loki_tenant_id": "team-a"},
		},
		{
			name:   "valid with bearer token",
			config: map[string]string{"loki_url": "https://loki.example.com", "loki_auth_type": LokiAuthBearer, "loki_bearer_token": "abc"},
		},
		{
			// With no method selected, an Authorization header is the operator's own
			// escape hatch for an auth scheme the selector does not model.
			name:   "authorization header with no auth method selected",
			config: map[string]string{"loki_url": "https://loki.example.com", "loki_headers": `{"Authorization":"Bearer abc"}`},
		},
		{
			name:    "basic auth without a password",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_auth_type": LokiAuthBasic, "loki_username": "u"},
			wantErr: "both required for basic authentication",
		},
		{
			name:    "bearer auth without a token",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_auth_type": LokiAuthBearer},
			wantErr: "loki_bearer_token is required",
		},
		{
			name:    "unknown auth type",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_auth_type": "mtls"},
			wantErr: "loki_auth_type must be one of",
		},
		{
			name:    "missing url",
			config:  map[string]string{"loki_username": "u", "loki_password": "p"},
			wantErr: "loki_url is required",
		},
		{
			name:    "url with a path",
			config:  map[string]string{"loki_url": "https://loki.example.com/explore"},
			wantErr: "must be the base URL only",
		},
		{
			name:    "url without a scheme",
			config:  map[string]string{"loki_url": "loki.example.com"},
			wantErr: "loki_url must",
		},
		{
			// An integration saved before the auth selector existed carries no
			// loki_auth_type; its credentials still have to be judged as basic auth.
			name:    "legacy row with a username but no password",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_username": "u"},
			wantErr: "both required for basic authentication",
		},
		{
			name:    "legacy row with a password but no username",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_password": "p"},
			wantErr: "both required for basic authentication",
		},
		{
			name:    "malformed headers",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_headers": "Authorization: Bearer abc"},
			wantErr: "must be a JSON object of string values",
		},
		{
			name:    "non-string header value",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_headers": `{"X-Retries": 3}`},
			wantErr: `value for header "X-Retries" must be a string`,
		},
		{
			// Rejected at save time so it cannot save as valid and then fail every
			// query — the divergence the shared parser exists to prevent.
			name:    "null header value",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_headers": `{"X-Empty": null}`},
			wantErr: `value for header "X-Empty" must be a string`,
		},
		{
			// Two auth methods at once is a misconfiguration that would otherwise
			// surface as a confusing 401, decided by header ordering.
			name:    "authorization header alongside a selected method",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_auth_type": LokiAuthBasic, "loki_username": "u", "loki_password": "p", "loki_headers": `{"Authorization":"Bearer abc"}`},
			wantErr: "use one or the other",
		},
		{
			name:    "rules url with a path",
			config:  map[string]string{"loki_url": "http://loki.example.com", "loki_rules_url": "http://ruler.example.com/rules"},
			wantErr: "loki_rules_url must be the base URL only",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := Loki{}.ValidateConfig(nil, lokiValues(tc.config), "acc-1")
			if tc.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			var joined string
			for _, err := range errs {
				joined += err.Error() + "\n"
			}
			assert.Contains(t, joined, tc.wantErr)
		})
	}
}

func TestLokiTestConnection(t *testing.T) {
	t.Run("probes buildinfo with credentials", func(t *testing.T) {
		var gotPath, gotTenant, gotExtra string
		var gotUser, gotPass string
		var gotAuthOK bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotUser, gotPass, gotAuthOK = r.BasicAuth()
			gotTenant = r.Header.Get("X-Scope-OrgID")
			gotExtra = r.Header.Get("X-Extra")
			_, _ = w.Write([]byte(`{"version":"3.4.1"}`))
		}))
		defer server.Close()

		err := Loki{}.TestConnection(nil, lokiValues(map[string]string{
			"loki_url":       server.URL,
			"loki_auth_type": LokiAuthBasic,
			"loki_username":  "1234",
			"loki_password":  "token",
			"loki_tenant_id": "team-a",
			"loki_headers":   `{"X-Extra":"yes"}`,
		}), "acc-1")
		require.NoError(t, err)

		// Not /ready: a loki-gateway only proxies /loki/... and 404s everything else.
		assert.Equal(t, "/loki/api/v1/status/buildinfo", gotPath)
		assert.True(t, gotAuthOK)
		assert.Equal(t, "1234", gotUser)
		assert.Equal(t, "token", gotPass)
		assert.Equal(t, "team-a", gotTenant)
		assert.Equal(t, "yes", gotExtra)
	})

	t.Run("maps status codes to actionable messages", func(t *testing.T) {
		cases := []struct {
			status int
			want   string
		}{
			{http.StatusUnauthorized, "invalid Loki credentials"},
			{http.StatusForbidden, "check loki_tenant_id"},
			{http.StatusNotFound, "check that loki_url points at the Loki query endpoint"},
			{http.StatusInternalServerError, "unexpected status: HTTP 500"},
		}
		for _, tc := range cases {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			err := Loki{}.TestConnection(nil, lokiValues(map[string]string{"loki_url": server.URL}), "acc-1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			server.Close()
		}
	})

	t.Run("probes the ruler only when a rules url is set", func(t *testing.T) {
		var paths []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths = append(paths, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		err := Loki{}.TestConnection(nil, lokiValues(map[string]string{"loki_url": server.URL}), "acc-1")
		require.NoError(t, err)
		assert.Equal(t, []string{"/loki/api/v1/status/buildinfo"}, paths)

		paths = nil
		err = Loki{}.TestConnection(nil, lokiValues(map[string]string{
			"loki_url":       server.URL,
			"loki_rules_url": server.URL,
		}), "acc-1")
		require.NoError(t, err)
		assert.Equal(t, []string{"/loki/api/v1/status/buildinfo", "/loki/api/v1/rules"}, paths)
	})

	t.Run("names the read-only ruler explicitly", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/loki/api/v1/rules" {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		err := Loki{}.TestConnection(nil, lokiValues(map[string]string{
			"loki_url":       server.URL,
			"loki_rules_url": server.URL,
		}), "acc-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read-only")
	})

	// Verified against Loki 3.4.1: a ruler holding no rule groups answers the listing
	// with 404 "no rule groups found". That is the state every deployment is in before
	// its first alert rule, so failing the connection test for it would block setup.
	t.Run("accepts a ruler with no rule groups yet", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/loki/api/v1/rules" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("no rule groups found"))
				return
			}
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()

		err := Loki{}.TestConnection(nil, lokiValues(map[string]string{
			"loki_url":       server.URL,
			"loki_rules_url": server.URL,
		}), "acc-1")
		assert.NoError(t, err)
	})
}

func TestNormalizeLokiURL(t *testing.T) {
	assert.Equal(t, "https://loki.example.com", normalizeLokiURL("  https://loki.example.com/explore?left=x  "))
	assert.Equal(t, "http://loki.example.com", normalizeLokiURL("http://loki.example.com/"))
	assert.Equal(t, "", normalizeLokiURL(""))
}
