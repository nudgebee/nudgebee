package integrations

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- endpoint construction (no I/O) ---------------------------------------

func TestConfluence_APIBase(t *testing.T) {
	cases := []struct {
		name string
		host string
		mode string
		want string
	}{
		{"cloud bare host", "https://acme.atlassian.net", ConfluenceAuthCloudAPIToken, "https://acme.atlassian.net/wiki/rest/api"},
		{"cloud trailing slash", "https://acme.atlassian.net/", ConfluenceAuthCloudAPIToken, "https://acme.atlassian.net/wiki/rest/api"},
		{"cloud host already carries /wiki", "https://acme.atlassian.net/wiki", ConfluenceAuthCloudAPIToken, "https://acme.atlassian.net/wiki/rest/api"},
		{"empty mode is treated as cloud", "https://acme.atlassian.net", "", "https://acme.atlassian.net/wiki/rest/api"},
		{"unknown mode is treated as cloud", "https://acme.atlassian.net", "carrier-pigeon", "https://acme.atlassian.net/wiki/rest/api"},

		{"dc pat has no /wiki", "https://wiki.acme.internal", ConfluenceAuthDataCenterPAT, "https://wiki.acme.internal/rest/api"},
		{"dc basic has no /wiki", "https://wiki.acme.internal", ConfluenceAuthDataCenterPassword, "https://wiki.acme.internal/rest/api"},
		{"dc context path is preserved", "https://wiki.acme.internal/confluence", ConfluenceAuthDataCenterPAT, "https://wiki.acme.internal/confluence/rest/api"},
		{"dc context path with trailing slash", "https://wiki.acme.internal/confluence/", ConfluenceAuthDataCenterPAT, "https://wiki.acme.internal/confluence/rest/api"},
		{"dc on a non-standard port", "https://wiki.acme.internal:8090", ConfluenceAuthDataCenterPAT, "https://wiki.acme.internal:8090/rest/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, confluenceAPIBase(c.host, c.mode))
		})
	}
}

func TestConfluence_AuthHeader(t *testing.T) {
	// base64("me@acme.com:tok") — Cloud and DC basic authenticate identically.
	const wantBasic = "Basic bWVAYWNtZS5jb206dG9r"

	assert.Equal(t, wantBasic, confluenceAuthHeader(ConfluenceAuthCloudAPIToken, "me@acme.com", "tok"))
	assert.Equal(t, wantBasic, confluenceAuthHeader(ConfluenceAuthDataCenterPassword, "me@acme.com", "tok"))
	assert.Equal(t, wantBasic, confluenceAuthHeader("", "me@acme.com", "tok"))

	// A personal access token is a bearer token and carries no username.
	assert.Equal(t, "Bearer tok", confluenceAuthHeader(ConfluenceAuthDataCenterPAT, "", "tok"))
	assert.Equal(t, "Bearer tok", confluenceAuthHeader(ConfluenceAuthDataCenterPAT, "ignored", "tok"))
}

func TestConfluence_AuthType_AbsentValueStaysCloud(t *testing.T) {
	// Integrations created before Data Center support carry no auth_type row.
	// They must keep behaving exactly as they do today.
	assert.Equal(t, ConfluenceAuthCloudAPIToken, confluenceAuthType(""))
	assert.Equal(t, ConfluenceAuthCloudAPIToken, confluenceAuthType("   "))
	assert.True(t, confluenceIsCloud(""))
	assert.True(t, confluenceNeedsUsername(""))
	assert.False(t, confluenceNeedsUsername(ConfluenceAuthDataCenterPAT))
}

func TestConfluence_ConfigSchema_UsernameIsConditional(t *testing.T) {
	schema := Confluence{}.ConfigSchema()

	assert.NotContains(t, schema.Required, "username", "a PAT config has no username, so it cannot be unconditionally required")
	assert.Contains(t, schema.Required, "token")
	assert.Contains(t, schema.Required, "host")

	username := schema.Properties["username"]
	require.NotNil(t, username.ShowWhen)
	require.NotNil(t, username.RequiredWhen)
	assert.Equal(t, []any{ConfluenceAuthCloudAPIToken, ConfluenceAuthDataCenterPassword}, username.ShowWhen["auth_type"])

	assert.Equal(t, ConfluenceAuthCloudAPIToken, schema.Properties["auth_type"].Default)
	assert.True(t, schema.Properties["token"].IsEncrypted)
}

// ----- config-shape validation (no I/O) -------------------------------------

func TestConfluence_ValidateConfig_UsernameRequiredOnlyWhenTheTypeUsesOne(t *testing.T) {
	errs := Confluence{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("auth_type", ConfluenceAuthDataCenterPassword),
		cv("host", "https://wiki.acme.internal"),
		cv("token", "secret"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "username is required")
}

func TestConfluence_ValidateConfig_RejectsAtlassianHostForDataCenter(t *testing.T) {
	errs := Confluence{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("auth_type", ConfluenceAuthDataCenterPAT),
		cv("host", "https://acme.atlassian.net"),
		cv("token", "pat"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "do not issue personal access tokens")
}

func TestConfluence_ValidateConfig_RejectsApiPathInHost(t *testing.T) {
	errs := Confluence{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("auth_type", ConfluenceAuthDataCenterPAT),
		cv("host", "https://wiki.acme.internal/rest/api"),
		cv("token", "pat"),
	}, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "remove the /rest/api path")
}

// ----- connection test ------------------------------------------------------

func confluenceCfg(host, mode string) []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{
		cv("auth_type", mode),
		cv("host", host),
		cv("username", "me@acme.com"),
		cv("token", "secret"),
	}
}

func TestConfluence_TestConnection_DataCenterPAT(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	errs := Confluence{}.ValidateConfig(nil, []core.IntegrationConfigValue{
		cv("auth_type", ConfluenceAuthDataCenterPAT),
		cv("host", srv.URL),
		cv("token", "pat-value"),
	}, "acc")

	assert.Empty(t, errs)
	assert.Equal(t, "Bearer pat-value", gotAuth)
	assert.Equal(t, "/rest/api/space", gotPath, "Data Center serves the API without the Cloud /wiki prefix")
}

func TestConfluence_TestConnection_CloudAPIToken(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	errs := Confluence{}.ValidateConfig(nil, confluenceCfg(srv.URL, ConfluenceAuthCloudAPIToken), "acc")

	assert.Empty(t, errs)
	assert.Equal(t, "Basic bWVAYWNtZS5jb206c2VjcmV0", gotAuth)
	assert.Equal(t, "/wiki/rest/api/space", gotPath)
}

func TestConfluence_TestConnection_UnauthorizedMessageIsTypeSpecific(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cases := []struct {
		mode string
		want string
	}{
		{ConfluenceAuthDataCenterPAT, "expired or revoked"},
		{ConfluenceAuthDataCenterPassword, "basic authentication is often disabled"},
		{ConfluenceAuthCloudAPIToken, "id.atlassian.com"},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			errs := Confluence{}.ValidateConfig(nil, confluenceCfg(srv.URL, c.mode), "acc")
			require.Len(t, errs, 1)
			assert.Contains(t, errs[0].Error(), c.want)
		})
	}
}

func TestConfluence_TestConnection_InterceptedByPortalReportsHTML(t *testing.T) {
	// A reverse proxy or SSO portal answering with a login page is the common
	// on-premise failure that otherwise surfaces as a JSON parse error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Sign in to continue</body></html>"))
	}))
	defer srv.Close()

	errs := Confluence{}.ValidateConfig(nil, confluenceCfg(srv.URL, ConfluenceAuthDataCenterPassword), "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "SSO portal or reverse proxy")
}

func TestConfluence_TestConnection_NotFoundCrossProbesOtherDeployment(t *testing.T) {
	// The instance is Data Center, but the user selected Cloud. The Cloud path
	// 404s while the Data Center path answers, which is enough to name the fix.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/wiki/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	errs := Confluence{}.ValidateConfig(nil, confluenceCfg(srv.URL, ConfluenceAuthCloudAPIToken), "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "Data Center / Server")
	assert.Contains(t, errs[0].Error(), "change the authentication method")
}

func TestConfluence_TestConnection_NotFoundEverywhereMentionsContextPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	errs := Confluence{}.ValidateConfig(nil, confluenceCfg(srv.URL, ConfluenceAuthDataCenterPAT), "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "context path")
}

func TestConfluence_TestConnection_NamespaceMustResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The space-key filter returns 200 with an empty list for an unknown key.
		if r.URL.Query().Get("spaceKey") != "" {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"key":"ENG"}]}`))
	}))
	defer srv.Close()

	cfg := append(confluenceCfg(srv.URL, ConfluenceAuthDataCenterPassword), cv("namespace", "NOPE"))
	errs := Confluence{}.ValidateConfig(nil, cfg, "acc")
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), `space "NOPE" does not exist`)
}
