package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

var (
	ghAcme = &workspace.ScmBinding{Tool: "github_execute", Config: "acme"}
	glAcme = &workspace.ScmBinding{Tool: "gitlab_execute", Config: "acme"}
)

// scmToken mints a real execution token and binds it to an integration, as
// commandEnv does for a gh/glab command.
func scmToken(t *testing.T, scm *workspace.ScmClaims) string {
	t.Helper()
	config.Config.LlmServerJwtSecret = "test-secret"
	security.SetTenantIdCacheForTest("scm-workspace", "scm-tenant")
	stubTenantAccounts(t, "scm-workspace")
	raw, err := workspace.ExecutionToken(security.NewRequestContextForTenantAccountAdmin("scm-tenant", "user", []string{"scm-workspace"}), "scm-workspace", "", time.Minute)
	require.NoError(t, err)
	if scm == nil {
		return raw
	}
	claims := &workspace.WorkspaceTokenClaims{}
	_, err = jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return []byte("test-secret"), nil })
	require.NoError(t, err)
	claims.Scm = scm
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-secret"))
	require.NoError(t, err)
	return signed
}

type upstreamCall struct {
	method, path, auth, privateToken, body string
}

// scmTestProxy points the proxy at an httptest upstream standing in for
// api.github.com (/api/v3) and its GraphQL endpoint.
func scmTestProxy(t *testing.T, provider string, status int, header map[string]string) (*scmProxy, *[]upstreamCall, string) {
	t.Helper()
	var calls []upstreamCall
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		calls = append(calls, upstreamCall{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("PRIVATE-TOKEN"), string(b)})
		for k, v := range header {
			w.Header().Set(k, strings.ReplaceAll(v, "{upstream}", "http://"+r.Host))
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(up.Close)
	upstream := &scmUpstream{provider: "GitHub", authHeader: "Authorization", authValue: "token real-github-token",
		prefixes: [][2]string{{"/api/v3", up.URL}, {"/api/graphql", up.URL + "/graphql"}}}
	if provider == workspace.ScmProviderGitlab {
		upstream = &scmUpstream{provider: "GitLab", authHeader: "PRIVATE-TOKEN", authValue: "real-gitlab-token",
			prefixes: [][2]string{{"/api/v4", up.URL + "/api/v4"}}}
	}
	p := &scmProxy{transport: http.DefaultTransport, resolve: func(*security.RequestContext, string, string, *workspace.ScmBinding) (*scmUpstream, error) {
		return upstream, nil
	}}
	return p, &calls, up.URL
}

func serveScm(p *scmProxy, method, path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://llm-server:8443"+path, strings.NewReader(body))
	req.Host = "llm-server:8443"
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	return rec
}

func TestScmProxyForwardsReadsWithTheRealToken(t *testing.T) {
	p, calls, upstreamURL := scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK,
		map[string]string{"Link": `<{upstream}/repositories/1/issues?page=2>; rel="next", <https://example.com/x>; rel="other"`})
	token := scmToken(t, &workspace.ScmClaims{Github: ghAcme})

	rec := serveScm(p, http.MethodGet, "/api/v3/repos/o/r/issues?per_page=100", "token "+token, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, *calls, 1)
	assert.Equal(t, "/repos/o/r/issues?per_page=100", (*calls)[0].path)
	assert.Equal(t, "token real-github-token", (*calls)[0].auth, "the workspace token is replaced by the integration's")
	link := rec.Header().Get("Link")
	assert.Contains(t, link, "<https://llm-server:8443/api/v3/repositories/1/issues?page=2>", "pagination stays on the proxy")
	assert.Contains(t, link, "<https://example.com/x>", "other hosts are left alone")
	assert.NotContains(t, link, upstreamURL)
}

func TestScmProxyRefusesBadTokens(t *testing.T) {
	p, calls, _ := scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK, nil)
	unbound := scmToken(t, nil)
	for name, auth := range map[string]string{
		"missing":          "",
		"garbage":          "token not-a-jwt",
		"not scm-bound":    "token " + unbound,
		"wrong secret jwt": "token eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOlsid29ya3NwYWNlLWV4ZWN1dGUiXX0.c2lnbmF0dXJl",
	} {
		rec := serveScm(p, http.MethodGet, "/api/v3/user", auth, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, name)
	}
	assert.Empty(t, *calls, "nothing reaches GitHub without a valid bound token")
}

func TestScmProxyReadOnlyGate(t *testing.T) {
	readToken := scmToken(t, &workspace.ScmClaims{Github: ghAcme})
	writeToken := scmToken(t, &workspace.ScmClaims{Github: ghAcme, Write: true})

	cases := []struct {
		name, method, path, body string
		allowedRead              bool
	}{
		{"REST read", http.MethodGet, "/api/v3/repos/o/r", "", true},
		{"REST comment", http.MethodPost, "/api/v3/repos/o/r/issues/1/comments", `{"body":"x"}`, false},
		{"REST delete", http.MethodDelete, "/api/v3/repos/o/r", "", false},
		{"GraphQL query", http.MethodPost, "/api/graphql", `{"query":"query PullRequestList { repository(owner:\"o\", name:\"r\") { id } }"}`, true},
		{"GraphQL shorthand query", http.MethodPost, "/api/graphql", `{"query":"{ viewer { login } }"}`, true},
		{"GraphQL mutation", http.MethodPost, "/api/graphql", `{"query":"mutation { addComment(input:{}) { clientMutationId } }"}`, false},
		{"mutation after a comment", http.MethodPost, "/api/graphql", `{"query":"# query\nmutation M { deleteRef(input:{}) { clientMutationId } }"}`, false},
		{"fragment first, then mutation", http.MethodPost, "/api/graphql", `{"query":"fragment f on Repository { id }\nmutation M { deleteRef(input:{}) { clientMutationId } }"}`, false},
		{"query and mutation, mutation named", http.MethodPost, "/api/graphql", `{"query":"query Q { viewer { login } } mutation M { deleteRef(input:{}) { clientMutationId } }","operationName":"M"}`, false},
		{"query and mutation, none named", http.MethodPost, "/api/graphql", `{"query":"query Q { viewer { login } } mutation M { deleteRef(input:{}) { clientMutationId } }"}`, false},
		{"query and mutation, query named", http.MethodPost, "/api/graphql", `{"query":"query Q { viewer { login } } mutation M { deleteRef(input:{}) { clientMutationId } }","operationName":"Q"}`, false},
		{"unknown operation name", http.MethodPost, "/api/graphql", `{"query":"query Q { viewer { login } }","operationName":"X"}`, false},
		{"not JSON", http.MethodPost, "/api/graphql", `mutation { x }`, false},
		{"subscription", http.MethodPost, "/api/graphql", `{"query":"subscription { x }"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, calls, _ := scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK, nil)
			rec := serveScm(p, tc.method, tc.path, "token "+readToken, tc.body)
			if tc.allowedRead {
				assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Len(t, *calls, 1)
				assert.Equal(t, tc.body, (*calls)[0].body, "the GraphQL body reaches GitHub unchanged")
			} else {
				assert.Equal(t, http.StatusForbidden, rec.Code)
				assert.Contains(t, rec.Body.String(), "not approved to change anything")
				assert.Empty(t, *calls)
			}

			p, calls, _ = scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK, nil)
			rec = serveScm(p, tc.method, tc.path, "token "+writeToken, tc.body)
			assert.Equal(t, http.StatusOK, rec.Code, "an approved write passes")
			assert.Len(t, *calls, 1)
		})
	}
}

func TestScmProxyOnlyServesApiPaths(t *testing.T) {
	p, calls, _ := scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK, nil)
	token := scmToken(t, &workspace.ScmClaims{Github: ghAcme, Write: true})
	for _, path := range []string{"/o/r.git/info/refs", "/api/v3x/repos", "/"} {
		rec := serveScm(p, http.MethodGet, path, "token "+token, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
	}
	rec := serveScm(p, http.MethodGet, "/api/v4/projects", "token "+token, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "a GitLab call on a token bound only to GitHub")
	assert.Contains(t, rec.Body.String(), "no gitlab integration")
	assert.Empty(t, *calls)
}

func TestScmProxyGitlabUsesPrivateToken(t *testing.T) {
	p, calls, _ := scmTestProxy(t, workspace.ScmProviderGitlab, http.StatusOK, nil)
	token := scmToken(t, &workspace.ScmClaims{Gitlab: glAcme})

	req := httptest.NewRequest(http.MethodGet, "https://llm-server:8443/api/v4/projects", nil)
	req.Header.Set("PRIVATE-TOKEN", token)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, *calls, 1)
	assert.Equal(t, "real-gitlab-token", (*calls)[0].privateToken)
	assert.Empty(t, (*calls)[0].auth)

	rec = serveScm(p, http.MethodGet, "/api/v4/projects/group%2Fsub%2Fproject/merge_requests?state=opened", "Bearer "+token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "/api/v4/projects/group%2Fsub%2Fproject/merge_requests?state=opened", (*calls)[1].path, "encoded project paths keep their encoding")

	rec = serveScm(p, http.MethodPost, "/api/v4/usage_data/track_event", "Bearer "+token, `{}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, "glab telemetry is a write")
}

func TestGithubUpstreamPrefixes(t *testing.T) {
	dotcom := &scmUpstream{prefixes: [][2]string{{"/api/v3", "https://api.github.com"}, {"/api/graphql", "https://api.github.com/graphql"}}}
	u, ok := dotcom.target("/api/graphql")
	require.True(t, ok)
	assert.Equal(t, "https://api.github.com/graphql", u.String())
	u, ok = dotcom.target("/api/v3/repos/o/r/actions/runs/1/logs")
	require.True(t, ok)
	assert.Equal(t, "https://api.github.com/repos/o/r/actions/runs/1/logs", u.String())
	assert.Equal(t, "<https://llm-server:8443/api/v3/repositories/9/pulls?page=3>; rel=\"next\"",
		dotcom.rewriteURLs("<https://api.github.com/repositories/9/pulls?page=3>; rel=\"next\"", "llm-server:8443"))
	assert.Equal(t, "https://productionresultssa1.blob.core.windows.net/x?sig=1",
		dotcom.rewriteURLs("https://productionresultssa1.blob.core.windows.net/x?sig=1", "llm-server:8443"),
		"signed log storage redirects are followed directly, without the token")
}

// Only the GraphQL endpoint itself gets the query-only exemption: a REST
// write whose last segment happens to be "graphql" is still a write.
func TestScmProxyGraphQLExemptionIsTheEndpointOnly(t *testing.T) {
	token := scmToken(t, &workspace.ScmClaims{Gitlab: glAcme})
	p, calls, _ := scmTestProxy(t, workspace.ScmProviderGitlab, http.StatusOK, nil)
	p.resolve = func(*security.RequestContext, string, string, *workspace.ScmBinding) (*scmUpstream, error) {
		return &scmUpstream{provider: "GitLab", authHeader: "PRIVATE-TOKEN", authValue: "real", prefixes: [][2]string{{"/api/v4", "http://127.0.0.1:1/api/v4"}, {"/api/graphql", "http://127.0.0.1:1/api/graphql"}}}, nil
	}
	for _, path := range []string{"/api/v4/groups/5/projects/ns%2Fgraphql", "/api/v4/projects/1/graphql", "/api/graphql/extra"} {
		rec := serveScm(p, http.MethodPost, path, "Bearer "+token, `{"query":"{ __typename }"}`)
		assert.Equal(t, http.StatusForbidden, rec.Code, path)
	}
	assert.Empty(t, *calls)
}

func TestScmProxyRefusesDotSegments(t *testing.T) {
	p, calls, _ := scmTestProxy(t, workspace.ScmProviderGithub, http.StatusOK, nil)
	token := scmToken(t, &workspace.ScmClaims{Github: ghAcme, Write: true})
	for _, path := range []string{"/api/v3/../user", "/api/v3/repos/./o", "/api/v3/%2e%2e/admin"} {
		rec := serveScm(p, http.MethodGet, path, "token "+token, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
	assert.Empty(t, *calls)
}

func TestScmProxyRewritesEveryLinkHeader(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Link", `<http://`+r.Host+`/r?page=2>; rel="next"`)
		w.Header().Add("Link", `<http://`+r.Host+`/r?page=9>; rel="last"`)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	p := &scmProxy{transport: http.DefaultTransport, resolve: func(*security.RequestContext, string, string, *workspace.ScmBinding) (*scmUpstream, error) {
		return &scmUpstream{provider: "GitHub", authHeader: "Authorization", authValue: "token real", prefixes: [][2]string{{"/api/v3", up.URL}}}, nil
	}}
	token := scmToken(t, &workspace.ScmClaims{Github: ghAcme})

	rec := serveScm(p, http.MethodGet, "/api/v3/r", "token "+token, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{
		`<https://llm-server:8443/api/v3/r?page=2>; rel="next"`,
		`<https://llm-server:8443/api/v3/r?page=9>; rel="last"`,
	}, rec.Header().Values("Link"), "every Link header is kept and rewritten")
}

func TestScmUpstreamRequiresHTTPS(t *testing.T) {
	gh := func(url string) toolcore.ToolConfig {
		return toolcore.ToolConfig{Values: []toolcore.ToolConfigValue{{Name: "url", Value: url}, {Name: "password", Value: "tok"}}}
	}
	gl := func(url string) toolcore.ToolConfig {
		return toolcore.ToolConfig{Values: []toolcore.ToolConfigValue{{Name: "url", Value: url}, {Name: "password", Value: "tok"}}}
	}
	for _, bad := range []string{"http://169.254.169.254", "http://127.0.0.1:8000", "http://api-server.nudgebee.svc:8000", "ftp://ghe.example.com"} {
		_, err := githubUpstream(context.Background(), gh(bad))
		assert.Error(t, err, bad)
		_, err = gitlabUpstream(gl(bad))
		assert.Error(t, err, bad)
	}
	for _, ok := range []string{"", "api.github.com", "https://ghe.example.com", "ghe.example.com"} {
		_, err := githubUpstream(context.Background(), gh(ok))
		assert.NoError(t, err, ok)
	}
	_, err := gitlabUpstream(gl("https://gitlab.example.com"))
	assert.NoError(t, err)
}

func TestScmDialControlRefusesLocalAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:443", "[::1]:443", "169.254.169.254:80", "0.0.0.0:443", "[fe80::1]:443", "224.0.0.1:443"} {
		assert.Error(t, scmDialControl("tcp", addr, nil), addr)
	}
	for _, addr := range []string{"10.0.0.5:443", "192.168.1.10:443", "140.82.112.3:443"} {
		assert.NoError(t, scmDialControl("tcp", addr, nil), addr, "private ranges stay allowed for on-prem GitHub Enterprise and GitLab")
	}
}

func TestScmRewriteMatchesLongestPrefix(t *testing.T) {
	up := &scmUpstream{prefixes: [][2]string{{"/api/v3", "https://api.github.com"}, {"/api/graphql", "https://api.github.com/graphql"}}}
	assert.Equal(t, "<https://llm-server:8443/api/graphql>; rel=\"x\"", up.rewriteURLs("<https://api.github.com/graphql>; rel=\"x\"", "llm-server:8443"))
}

// One token can carry both integrations (a script calling gh and glab); the
// proxy routes each call to the right one: /api/v4 and a GraphQL call sent
// with PRIVATE-TOKEN are GitLab's, the rest GitHub's.
func TestScmProxyRoutesEachCallToItsProvider(t *testing.T) {
	var got []string
	upstreams := map[string]*httptest.Server{}
	for _, provider := range []string{workspace.ScmProviderGithub, workspace.ScmProviderGitlab} {
		provider := provider
		upstreams[provider] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = append(got, provider+" "+r.URL.Path+" auth="+r.Header.Get("Authorization")+" priv="+r.Header.Get("PRIVATE-TOKEN"))
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(upstreams[provider].Close)
	}
	p := &scmProxy{transport: http.DefaultTransport, resolve: func(_ *security.RequestContext, _ string, provider string, b *workspace.ScmBinding) (*scmUpstream, error) {
		if provider == workspace.ScmProviderGitlab {
			return &scmUpstream{provider: "GitLab", authHeader: "PRIVATE-TOKEN", authValue: "real-gl:" + b.Config, prefixes: [][2]string{{"/api/v4", upstreams[provider].URL + "/api/v4"}, {"/api/graphql", upstreams[provider].URL + "/api/graphql"}}}, nil
		}
		return &scmUpstream{provider: "GitHub", authHeader: "Authorization", authValue: "token real-gh:" + b.Config, prefixes: [][2]string{{"/api/v3", upstreams[provider].URL}, {"/api/graphql", upstreams[provider].URL + "/graphql"}}}, nil
	}}
	token := scmToken(t, &workspace.ScmClaims{
		Github: &workspace.ScmBinding{Tool: "github_execute", Config: "acme-gh"},
		Gitlab: &workspace.ScmBinding{Tool: "gitlab_execute", Config: "acme-gl"},
	})

	require.Equal(t, http.StatusOK, serveScm(p, http.MethodGet, "/api/v3/user", "token "+token, "").Code)
	require.Equal(t, http.StatusOK, serveScm(p, http.MethodPost, "/api/graphql", "token "+token, `{"query":"{ viewer { login } }"}`).Code)
	req := httptest.NewRequest(http.MethodGet, "https://llm-server:8443/api/v4/projects", nil)
	req.Header.Set("PRIVATE-TOKEN", token)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	req = httptest.NewRequest(http.MethodPost, "https://llm-server:8443/api/graphql", strings.NewReader(`{"query":"{ currentUser { username } }"}`))
	req.Header.Set("PRIVATE-TOKEN", token)
	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, []string{
		"github /user auth=token real-gh:acme-gh priv=",
		"github /graphql auth=token real-gh:acme-gh priv=",
		"gitlab /api/v4/projects auth= priv=real-gl:acme-gl",
		"gitlab /api/graphql auth= priv=real-gl:acme-gl",
	}, got)
}
