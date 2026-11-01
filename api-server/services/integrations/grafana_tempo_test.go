package integrations

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tempoValues(kv map[string]string) []core.IntegrationConfigValue {
	out := make([]core.IntegrationConfigValue, 0, len(kv))
	for k, v := range kv {
		out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
	}
	return out
}

func TestGrafanaTempo_SchemaDeclaresOnlyTheTraceDefault(t *testing.T) {
	s := GrafanaTempo{}.ConfigSchema()
	assert.True(t, s.Testable)
	assert.ElementsMatch(t, []string{GrafanaTempoConfigURL, GrafanaTempoConfigAuthType}, s.Required)
	assert.Contains(t, s.Properties, core.DefaultTraceProvider)
	assert.NotContains(t, s.Properties, core.DefaultMetricsProvider)
	assert.NotContains(t, s.Properties, core.DefaultLogProvider)
	assert.True(t, s.Properties[GrafanaTempoConfigPassword].IsEncrypted)
	assert.True(t, s.Properties[GrafanaTempoConfigToken].IsEncrypted)
	assert.Equal(t, IntegrationGrafanaTempo, GrafanaTempo{}.Name())
	assert.Equal(t, core.IntegrationCategoryObservabilityPlatform, GrafanaTempo{}.Category())
}

func TestGrafanaTempo_ValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]string
		wantErr string
	}{
		{"none auth ok", map[string]string{GrafanaTempoConfigURL: "http://localhost:3200", GrafanaTempoConfigAuthType: "none"}, ""},
		{"path prefix ok", map[string]string{GrafanaTempoConfigURL: "https://gw.example.com/tempo", GrafanaTempoConfigAuthType: "none"}, ""},
		{"missing url", map[string]string{GrafanaTempoConfigAuthType: "none"}, "tempo_url is required"},
		{"bad scheme", map[string]string{GrafanaTempoConfigURL: "localhost:3200", GrafanaTempoConfigAuthType: "none"}, "must start with http"},
		{"basic needs user", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "basic", GrafanaTempoConfigPassword: "p"}, "tempo_username is required"},
		{"basic needs password", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "basic", GrafanaTempoConfigUsername: "u"}, "tempo_password is required"},
		{"bearer needs token", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "bearer"}, "tempo_token is required"},
		{"unknown auth", map[string]string{GrafanaTempoConfigURL: "http://t", GrafanaTempoConfigAuthType: "sigv4"}, "tempo_auth_type must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := GrafanaTempo{}.ValidateConfig(nil, tempoValues(tc.values), "acc")
			if tc.wantErr == "" {
				assert.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			assert.Contains(t, errs[0].Error(), tc.wantErr)
		})
	}
}

func TestGrafanaTempoConfig_Headers(t *testing.T) {
	basic := GrafanaTempoConfig{AuthType: GrafanaTempoAuthBasic, Username: "u", Password: "p", TenantID: "team-a"}
	assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte("u:p")), basic.Headers()["Authorization"])
	assert.Equal(t, "team-a", basic.Headers()["X-Scope-OrgID"])

	bearer := GrafanaTempoConfig{AuthType: GrafanaTempoAuthBearer, Token: "tok"}
	assert.Equal(t, "Bearer tok", bearer.Headers()["Authorization"])

	none := GrafanaTempoConfig{AuthType: GrafanaTempoAuthNone}
	assert.NotContains(t, none.Headers(), "Authorization")
	assert.NotContains(t, none.Headers(), "X-Scope-OrgID")
}

func TestGrafanaTempo_TestConnection(t *testing.T) {
	cases := []struct {
		name     string
		echo     int
		tags     int
		tagsBody string
		wantErr  string
	}{
		{"ok", 200, 200, `{"scopes":[]}`, ""},
		{"auth rejected", 401, 200, "", "rejected the credentials"},
		{"not a tempo endpoint", 404, 200, "", "path prefix"},
		{"tenant missing", 200, 401, "no org id", "tempo_tenant_id"},
		{"gateway unavailable", 200, 502, "<html><body>502 Bad Gateway</body></html>", "HTTP 502"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/echo":
					w.WriteHeader(tc.echo)
					_, _ = w.Write([]byte("echo"))
				case "/api/v2/search/tags":
					w.WriteHeader(tc.tags)
					_, _ = w.Write([]byte(tc.tagsBody))
				default:
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			err := GrafanaTempo{}.TestConnection(nil, tempoValues(map[string]string{
				GrafanaTempoConfigURL: srv.URL, GrafanaTempoConfigAuthType: "none",
			}), "acc")
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			if tc.name == "gateway unavailable" {
				assert.NotContains(t, err.Error(), "<html>", "the upstream gateway body must not be echoed back")
			}
		})
	}
}

func TestGrafanaTempo_TestConnectionSendsTenantAndAuth(t *testing.T) {
	var gotOrg, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrg, gotAuth = r.Header.Get("X-Scope-OrgID"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"scopes":[]}`))
	}))
	defer srv.Close()
	err := GrafanaTempo{}.TestConnection(nil, tempoValues(map[string]string{
		GrafanaTempoConfigURL: srv.URL + "/", GrafanaTempoConfigAuthType: "bearer",
		GrafanaTempoConfigToken: "tok", GrafanaTempoConfigTenantID: "team-a",
	}), "acc")
	require.NoError(t, err)
	assert.Equal(t, "team-a", gotOrg)
	assert.Equal(t, "Bearer tok", gotAuth)
}

func TestGetGrafanaTempoConfig_RequiresAccount(t *testing.T) {
	_, err := GetGrafanaTempoConfig(nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "account_id is required")
}
