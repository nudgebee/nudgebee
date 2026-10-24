package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// The SCM proxy keeps GitHub and GitLab tokens out of the workspace (#41852).
// gh and glab in a workspace command are pointed at it as if it were a GitHub
// Enterprise / self-hosted GitLab server and authenticate with the command's
// execution token, which names the integration. The proxy checks the token,
// replaces it with the integration's real token and forwards the call. A call
// that was not approved to change anything may only read.

const (
	scmProxyMaxGraphQLBody = 1 << 20
	scmAppTokenTTL         = 50 * time.Minute
)

// scmUpstream is where one integration's calls go, with the credential the
// proxy adds. prefixes maps a proxy path prefix to the upstream URL prefix;
// the same pairs, reversed, rewrite Link and Location headers.
type scmUpstream struct {
	provider   string
	prefixes   [][2]string // {proxy prefix, upstream prefix}
	authHeader string
	authValue  string
}

type scmProxy struct {
	transport http.RoundTripper
	// resolve returns the upstream for a verified token; replaceable in tests.
	resolve func(ctx *security.RequestContext, accountId, provider string, binding *workspace.ScmBinding) (*scmUpstream, error)
}

// NewScmProxyHandler is the handler served on the SCM proxy's TLS port.
func NewScmProxyHandler() http.Handler {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.MaxIdleConns = 100
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 2 * time.Minute
	// Nearly every call goes to one host (api.github.com or the GitLab
	// server); Go's default of 2 idle connections per host would churn them.
	transport.MaxIdleConnsPerHost = 32
	// The upstream comes from an integration's URL, and llm-server has no
	// egress policy of its own: never connect to loopback or link-local
	// addresses (llm-server itself, cloud metadata). Checked on the resolved
	// address, so DNS that points a name at them is caught too. Private
	// ranges stay allowed: on-prem GitHub Enterprise and GitLab live there.
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: scmDialControl}
	transport.DialContext = dialer.DialContext
	return &scmProxy{transport: transport, resolve: resolveScmUpstream}
}

func scmDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("scm proxy: dial %s: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("scm proxy: refusing to connect to %s", host)
	}
	return nil
}

func (p *scmProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, claims, err := authorizeScmRequest(r)
	if err != nil {
		scmProxyError(w, http.StatusUnauthorized, err.Error())
		return
	}
	provider := scmRequestProvider(r)
	logger := slog.Default().With("provider", provider, "account_id", claims.AccountId, "method", r.Method, "path", r.URL.Path, "write", claims.Scm.Write)
	binding := claims.Scm.Binding(provider)
	if binding == nil {
		logger.Info("scm proxy: no integration bound for this provider")
		scmProxyError(w, http.StatusForbidden, "no "+provider+" integration is available to this command")
		return
	}
	up, err := p.resolve(ctx, claims.AccountId, provider, binding)
	if err != nil {
		logger.Warn("scm proxy: integration unavailable", "error", err)
		scmProxyError(w, http.StatusBadGateway, "the "+provider+" integration for this command is not available")
		return
	}
	if hasDotSegment(r.URL.Path) {
		scmProxyError(w, http.StatusBadRequest, "path segments . and .. are not allowed")
		return
	}
	// The escaped path, so GitLab project paths (group%2Fproject) and
	// GitHub content paths keep their encoding.
	target, ok := up.target(r.URL.EscapedPath())
	if !ok {
		scmProxyError(w, http.StatusNotFound, "not a "+up.provider+" API path")
		return
	}
	target.RawQuery = r.URL.RawQuery
	if !claims.Scm.Write {
		readOnly, reason := scmReadOnlyRequest(r, r.URL.EscapedPath() == "/api/graphql")
		if !readOnly {
			logger.Info("scm proxy: refused a change in a read-only call", "reason", reason)
			scmProxyError(w, http.StatusForbidden, "This command was not approved to change anything in "+up.provider+" ("+reason+"). Ask for approval to make the change, or use a read-only command.")
			return
		}
	}

	proxyHost := r.Host
	rp := &httputil.ReverseProxy{
		Transport: p.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = target
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("PRIVATE-TOKEN")
			pr.Out.Header.Del("JOB-TOKEN")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Set(up.authHeader, up.authValue)
		},
		ModifyResponse: func(resp *http.Response) error {
			for _, h := range []string{"Link", "Location"} {
				values := resp.Header.Values(h)
				if len(values) == 0 {
					continue
				}
				rewritten := make([]string, len(values))
				for i, v := range values {
					rewritten[i] = up.rewriteURLs(v, proxyHost)
				}
				resp.Header[http.CanonicalHeaderKey(h)] = rewritten
			}
			logger.Info("scm proxy: call", "status", resp.StatusCode)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Warn("scm proxy: upstream call failed", "error", err)
			scmProxyError(w, http.StatusBadGateway, "could not reach "+up.provider)
		},
	}
	rp.ServeHTTP(w, r)
}

// hasDotSegment reports whether a decoded path has a "." or ".." segment,
// which an upstream would resolve out of the mapped API prefix.
func hasDotSegment(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// scmRequestProvider tells GitHub calls from GitLab ones: /api/v4 is GitLab's
// REST API; the shared /api/graphql is GitLab's when glab sent its token as
// PRIVATE-TOKEN (gh sends "Authorization: token"); everything else is GitHub's.
func scmRequestProvider(r *http.Request) string {
	path := r.URL.EscapedPath()
	if path == "/api/v4" || strings.HasPrefix(path, "/api/v4/") {
		return workspace.ScmProviderGitlab
	}
	if path == "/api/graphql" && r.Header.Get("PRIVATE-TOKEN") != "" {
		return workspace.ScmProviderGitlab
	}
	return workspace.ScmProviderGithub
}

// target maps a proxy path to the upstream URL; the caller adds the query.
func (u *scmUpstream) target(path string) (*url.URL, bool) {
	for _, pair := range u.prefixes {
		if path == pair[0] || strings.HasPrefix(path, pair[0]+"/") {
			out, err := url.Parse(pair[1] + strings.TrimPrefix(path, pair[0]))
			if err != nil {
				return nil, false
			}
			return out, true
		}
	}
	return nil, false
}

// rewriteURLs points upstream API URLs in a Link or Location header back at
// the proxy, so pagination and API redirects stay authenticated. Other hosts
// (signed log and artifact storage) are left alone.
func (u *scmUpstream) rewriteURLs(value, proxyHost string) string {
	// Longest upstream prefix first, so api.github.com/graphql is not taken
	// for api.github.com plus a /graphql path.
	pairs := slices.Clone(u.prefixes)
	slices.SortFunc(pairs, func(a, b [2]string) int { return len(b[1]) - len(a[1]) })
	for _, pair := range pairs {
		value = strings.ReplaceAll(value, pair[1], "https://"+proxyHost+pair[0])
	}
	return value
}

// authorizeScmRequest checks the execution token gh or glab sent and returns
// the caller's context. The token must be bound to an integration.
func authorizeScmRequest(r *http.Request) (*security.RequestContext, *workspace.WorkspaceTokenClaims, error) {
	raw := scmRequestToken(r)
	if raw == "" {
		return nil, nil, fmt.Errorf("missing token")
	}
	token, err := jwt.ParseWithClaims(raw, &workspace.WorkspaceTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(config.Config.LlmServerJwtSecret), nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("invalid or expired token")
	}
	claims, ok := token.Claims.(*workspace.WorkspaceTokenClaims)
	if !ok || !token.Valid || !slices.Contains(claims.Audience, workspace.AudienceWorkspaceExecute) ||
		claims.Scm == nil || claims.Grants == nil || claims.TenantId == "" || claims.AccountId == "" {
		return nil, nil, fmt.Errorf("token is not valid for this proxy")
	}
	tenantId, err := security.GetTenantIdFromAccountId(claims.AccountId)
	if err != nil || tenantId != claims.TenantId {
		return nil, nil, fmt.Errorf("token does not match tenant")
	}
	sc, err := security.NewSecurityContextFromWorkspaceGrants(tenantId, *claims.Grants)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid token grants")
	}
	ctx := security.NewRequestContext(r.Context(), sc, slog.Default(), nil, nil)
	if claims.Grants.GrantScoped {
		ctx = security.WithGrantScopedRun(ctx)
	}
	return ctx, claims, nil
}

// scmRequestToken reads the token in the forms gh ("token X") and glab
// (PRIVATE-TOKEN, or "Bearer X") send.
func scmRequestToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("PRIVATE-TOKEN")); v != "" {
		return v
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	for _, scheme := range []string{"token ", "Bearer ", "bearer "} {
		if strings.HasPrefix(auth, scheme) {
			return strings.TrimSpace(auth[len(scheme):])
		}
	}
	return ""
}

// scmReadOnlyRequest reports whether a call only reads: GET/HEAD/OPTIONS, or
// a POST to the GraphQL endpoint itself (graphQL) whose operations are all
// queries. reason says why not.
func scmReadOnlyRequest(r *http.Request, graphQL bool) (bool, string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true, ""
	}
	if r.Method != http.MethodPost || !graphQL {
		return false, r.Method + " request"
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, scmProxyMaxGraphQLBody+1))
	_ = r.Body.Close()
	if err != nil || len(body) > scmProxyMaxGraphQLBody {
		return false, "unreadable GraphQL request"
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if !graphQLQueryOnly(body) {
		return false, "GraphQL mutation"
	}
	return true, ""
}

// graphQLQueryOnly reports whether a GraphQL request body holds only queries.
// A document with any mutation or subscription counts as a change even when
// operationName picks a query, so a parser difference upstream cannot run
// it. Anything unparsable counts as a change.
func graphQLQueryOnly(body []byte) bool {
	var req struct {
		Query         string `json:"query"`
		OperationName string `json:"operationName"`
	}
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Query) == "" {
		return false
	}
	doc, err := parser.ParseQuery(&ast.Source{Input: req.Query})
	if err != nil || len(doc.Operations) == 0 {
		return false
	}
	for _, op := range doc.Operations {
		if op.Operation != ast.Query {
			return false
		}
	}
	if req.OperationName != "" && doc.Operations.ForName(req.OperationName) == nil {
		return false
	}
	return true
}

func scmProxyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// resolveScmUpstream loads the integration the token names, as the user it
// was issued to, and returns where its calls go with its real token.
func resolveScmUpstream(ctx *security.RequestContext, accountId, provider string, binding *workspace.ScmBinding) (*scmUpstream, error) {
	tool, ok := toolcore.GetNBTool(accountId, binding.Tool)
	if !ok {
		return nil, fmt.Errorf("unknown tool %q", binding.Tool)
	}
	configs, err := toolcore.ListToolConfigs(ctx, accountId, tool)
	if err != nil {
		return nil, fmt.Errorf("list %s configs: %w", binding.Tool, err)
	}
	idx := slices.IndexFunc(configs, func(c toolcore.ToolConfig) bool { return c.Name == binding.Config })
	if idx < 0 {
		return nil, fmt.Errorf("integration %q not found", binding.Config)
	}
	cfg := configs[idx]
	switch provider {
	case workspace.ScmProviderGithub:
		return githubUpstream(ctx.GetContext(), cfg)
	case workspace.ScmProviderGitlab:
		return gitlabUpstream(cfg)
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

func githubUpstream(ctx context.Context, cfg toolcore.ToolConfig) (*scmUpstream, error) {
	apiUrl, token, err := cachedGithubToken(ctx, cfg)
	if err != nil {
		return nil, err
	}
	up := &scmUpstream{provider: "GitHub", authHeader: "Authorization", authValue: "token " + token}
	root := strings.TrimSuffix(strings.TrimSpace(apiUrl), "/")
	if root == "" || strings.Contains(root, "api.github.com") || strings.TrimPrefix(strings.TrimPrefix(root, "https://"), "http://") == "github.com" {
		up.prefixes = [][2]string{
			{"/api/v3", "https://api.github.com"},
			{"/api/graphql", "https://api.github.com/graphql"},
			{"/api/uploads", "https://uploads.github.com"},
		}
		return up, nil
	}
	if !strings.Contains(root, "://") {
		root = "https://" + root
	}
	if err := requireHTTPS(root); err != nil {
		return nil, err
	}
	root = strings.TrimSuffix(root, "/api/v3")
	up.prefixes = [][2]string{
		{"/api/v3", root + "/api/v3"},
		{"/api/graphql", root + "/api/graphql"},
		{"/api/uploads", root + "/api/uploads"},
	}
	return up, nil
}

func gitlabUpstream(cfg toolcore.ToolConfig) (*scmUpstream, error) {
	base, token := tools.GitlabConfigFields(cfg)
	if token == "" {
		return nil, fmt.Errorf("gitlab integration has no token")
	}
	root := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if root == "" {
		root = "https://gitlab.com"
	}
	if !strings.Contains(root, "://") {
		root = "https://" + root
	}
	if err := requireHTTPS(root); err != nil {
		return nil, err
	}
	return &scmUpstream{
		provider:   "GitLab",
		authHeader: "PRIVATE-TOKEN",
		authValue:  token,
		prefixes: [][2]string{
			{"/api/v4", root + "/api/v4"},
			{"/api/graphql", root + "/api/graphql"},
		},
	}, nil
}

// requireHTTPS refuses an integration URL that is not https with a host: the
// proxy sends the integration's token there.
func requireHTTPS(root string) error {
	u, err := url.Parse(root)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("integration URL %q must be https", root)
	}
	return nil
}

// GitHub App installation tokens last an hour; minting one per proxied call
// would add a GitHub round trip to every gh request.
var (
	scmAppTokensMu sync.Mutex
	scmAppTokens   = map[string]scmCachedToken{}
)

type scmCachedToken struct {
	token   string
	expires time.Time
}

func cachedGithubToken(ctx context.Context, cfg toolcore.ToolConfig) (apiUrl, token string, err error) {
	authType, apiUrl, password := tools.GithubConfigFields(cfg)
	if authType != "application" {
		if password == "" {
			return "", "", fmt.Errorf("github integration has no token")
		}
		return apiUrl, password, nil
	}
	key := apiUrl + "|" + password
	scmAppTokensMu.Lock()
	cached, ok := scmAppTokens[key]
	scmAppTokensMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return apiUrl, cached.token, nil
	}
	token, err = tools.ResolveGithubToken(ctx, authType, apiUrl, password)
	if err != nil {
		return "", "", fmt.Errorf("github app token: %w", err)
	}
	scmAppTokensMu.Lock()
	now := time.Now()
	for k, v := range scmAppTokens { // drop expired tokens, so integrations that are gone do not linger
		if now.After(v.expires) {
			delete(scmAppTokens, k)
		}
	}
	scmAppTokens[key] = scmCachedToken{token: token, expires: now.Add(scmAppTokenTTL)}
	scmAppTokensMu.Unlock()
	return apiUrl, token, nil
}

// StartWorkspaceProxyTLS serves the workspace proxy's TLS port (the SCM
// route) when LlmServerWorkspaceScmEnabled is set, and returns the server so
// shutdown can stop it (nil when not started). A failure is logged, not fatal:
// gh and glab calls then fail, everything else works.
func StartWorkspaceProxyTLS(ctx context.Context) *http.Server {
	if !config.Config.LlmServerWorkspaceScmEnabled {
		return nil
	}
	getCert, err := workspace.WorkspaceProxyCertificates(ctx)
	if err != nil {
		slog.Error("workspace proxy tls: not started, no certificate", "error", err)
		return nil
	}
	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", config.Config.LlmServerWorkspaceProxyTLSPort),
		Handler:           NewScmProxyHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate: getCert,
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"http/1.1"},
		},
	}
	go func() {
		slog.Info("workspace proxy tls: listening", "addr", srv.Addr, "host", workspace.WorkspaceProxyTLSHost())
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			slog.Error("workspace proxy tls: listener stopped", "error", err)
		}
	}()
	return srv
}
