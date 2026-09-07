// Package promclient is the HTTP client for a user-configured (direct, non-agent)
// Prometheus-compatible endpoint: config keys, auth schemes (basic, bearer, AWS
// SigV4, Coralogix, Azure AD) and the request helpers.
//
// It is a leaf package on purpose. integrations imports eventrule, and eventrule
// imports observability/alertrule, so alertrule cannot import integrations to
// reach this client — yet the ruler-API alert-rule writer has to authenticate
// exactly the way metric queries do. Keeping the client here, with no nudgebee
// imports, lets both integrations (which wraps it with the integration-row loader)
// and alertrule use the one implementation.
package promclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// Config keys for the user-source Prometheus integration. They mirror the
// settings the k8s agent already accepts (PROMETHEUS_URL, PROMETHEUS_HEADERS and
// the managed-provider auth env vars in the agent's config package), so an
// operator moving a cluster from agent-relayed to direct queries transcribes the
// same values.
const (
	PrometheusURLKey          = "prometheus_url"
	PrometheusAuthTypeKey     = "auth_type"
	PrometheusUsernameKey     = "prometheus_username"
	PrometheusPasswordKey     = "prometheus_password"
	PrometheusBearerTokenKey  = "prometheus_bearer_token"
	PrometheusExtraHeadersKey = "prometheus_extra_headers"

	// PrometheusAdditionalLabelsKey mirrors the k8s agent's
	// prometheusAdditionalLabels: the label matcher the query builders' __CLUSTER__
	// placeholder expands to. The agent reports its own via connection_status; a
	// direct connection has no agent, so it is configured here instead.
	PrometheusAdditionalLabelsKey = "prometheus_additional_labels"

	PrometheusAWSAccessKeyIDKey     = "aws_access_key_id"
	PrometheusAWSSecretAccessKeyKey = "aws_secret_access_key"
	PrometheusAWSRegionKey          = "aws_region"
	PrometheusAWSServiceNameKey     = "aws_service_name"

	PrometheusCoralogixTokenKey = "coralogix_token"

	PrometheusAzureClientIDKey     = "azure_client_id"
	PrometheusAzureClientSecretKey = "azure_client_secret"
	PrometheusAzureTenantIDKey     = "azure_tenant_id"
	PrometheusAzureResourceKey     = "azure_resource"

	// Ruler settings. A hosted endpoint answers PromQL but does not necessarily
	// let anyone write alert rules: Mimir, Cortex and Grafana Cloud expose the
	// Cortex ruler API; plain Prometheus, Thanos and VictoriaMetrics do not.
	// The operator states which they have, and where the ruler lives when it is
	// a separate service from the query endpoint.
	PrometheusRulerTypeKey = "prometheus_ruler_type"
	PrometheusRulerURLKey  = "prometheus_ruler_url"
)

// Supported prometheus_ruler_type values.
const (
	PrometheusRulerNone        = "none"
	PrometheusRulerMimirCortex = "mimir_cortex"
)

// Supported auth_type values. These are exactly the schemes the agent supports,
// minus Azure IMDS managed identity: that flow mints a token from the instance
// metadata endpoint of the machine making the call, which for a direct
// integration is a Nudgebee server, not the customer's Azure VM.
const (
	PrometheusAuthNone        = "none"
	PrometheusAuthBasic       = "basic"
	PrometheusAuthBearerToken = "bearer_token"
	PrometheusAuthAWSSigV4    = "aws_sigv4"
	PrometheusAuthCoralogix   = "coralogix"
	PrometheusAuthAzureAD     = "azure_ad"
)

const (
	// PrometheusDefaultAWSService is Amazon Managed Prometheus' SigV4 service name.
	PrometheusDefaultAWSService = "aps"
	// PrometheusDefaultAzureResource is the audience for Azure Monitor managed Prometheus.
	PrometheusDefaultAzureResource = "https://prometheus.monitor.azure.com"
)

// azureTokenEndpointBase is the Azure AD login host. A package variable rather
// than a constant so tests can point the client-credentials flow at an httptest
// server.
var azureTokenEndpointBase = "https://login.microsoftonline.com"

// prometheusHTTPClient is shared across queries; per-call deadlines come from
// the caller's context.
var prometheusHTTPClient = &http.Client{Timeout: 60 * time.Second}

// emptyPayloadHash is SHA-256 of the empty string: the SigV4 payload hash for
// every body-less request, which is all of them except ruler writes.
var emptyPayloadHash = func() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}()

// PrometheusUserConfig is the resolved connection for a user-configured
// (direct, non-agent) Prometheus-compatible endpoint.
type PrometheusUserConfig struct {
	URL      string
	AuthType string

	Username    string
	Password    string
	BearerToken string

	// ExtraHeaders are sent on every request regardless of AuthType — the same
	// contract as the agent's PROMETHEUS_HEADERS. They carry multi-tenant
	// routing headers (X-Scope-OrgID for Cortex/Mimir) that a managed auth
	// scheme does not replace.
	ExtraHeaders http.Header

	// AdditionalLabels is the ready-to-substitute PromQL matcher fragment
	// (e.g. `cluster="prod"`), already validated and escaped. Empty when unset.
	AdditionalLabels string

	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSRegion          string
	AWSService         string

	CoralogixToken string

	AzureClientID     string
	AzureClientSecret string
	AzureTenantID     string
	AzureResource     string

	// RulerType is one of the PrometheusRuler* values; RulerURL is the ruler's
	// base URL, defaulting to URL when the ruler shares the query endpoint.
	RulerType string
	RulerURL  string
}

// ParsePrometheusHeaders turns the operator's raw header block into an
// http.Header. Two forms are accepted.
//
// A JSON object -- {"X-Scope-OrgID": "tenant-1"} -- matching the shape
// llm_extra_headers already uses. A value may be a string or an array of
// strings, so a header that repeats survives the round trip.
//
// Otherwise, one "Header: value" per line, which is
// what the UI's multiline field produces. A line is ALSO split on commas, but
// only when every resulting part is itself a "Header: value" — that accepts the
// agent's PROMETHEUS_HEADERS env form pasted verbatim
// ("X-Scope-OrgID: t1, X-Other: v") without mangling a single header whose value
// legitimately contains commas ("X-List: a, b, c"). A value containing both a
// comma and a colon is the one case the heuristic cannot resolve; write those
// one per line, or use the JSON form.
func ParsePrometheusHeaders(raw string) (http.Header, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// A leading brace is unambiguous -- no "Header: value" line can start with
	// one -- so commit to JSON and report JSON errors, rather than falling back
	// to the line parser and complaining about a missing colon.
	if strings.HasPrefix(raw, "{") {
		return parsePrometheusHeadersJSON(raw)
	}

	var entries []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		if parts, ok := splitHeaderList(line); ok {
			entries = append(entries, parts...)
			continue
		}
		entries = append(entries, line)
	}

	headers := http.Header{}
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !found || name == "" {
			return nil, fmt.Errorf("invalid header %q: expected \"Header: value\"", entry)
		}
		headers.Add(name, value)
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}

// splitHeaderList splits a comma-separated header list, reporting false when any
// part is not itself a "Header: value" — in which case the commas belong to a
// single header's value and the line must be kept whole.
// parsePrometheusHeadersJSON reads the {"Header": "value"} form. An array value
// expresses a header that repeats, which the line form gets from writing the
// same name twice.
func parsePrometheusHeadersJSON(raw string) (http.Header, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, fmt.Errorf("invalid JSON header object: %w", err)
	}

	headers := http.Header{}
	for name, value := range obj {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("invalid header: name must not be empty")
		}
		switch v := value.(type) {
		case string:
			headers.Add(name, strings.TrimSpace(v))
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("invalid header %q: array values must be strings", name)
				}
				headers.Add(name, strings.TrimSpace(s))
			}
		default:
			return nil, fmt.Errorf("invalid header %q: value must be a string or an array of strings", name)
		}
	}
	if len(headers) == 0 {
		return nil, nil
	}
	return headers, nil
}

// promqlLabelNameRe and escapePromQLValue duplicate observability's unexported
// equivalents. observability imports this package, so the reverse import would
// be a cycle; the two are three lines each and the PromQL grammar they encode
// does not move.
var promqlLabelNameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

func escapePromQLValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// ParsePrometheusAdditionalLabels turns the configured {"cluster": "prod"} JSON
// object into the PromQL matcher fragment `cluster="prod"`. Keys are validated
// as label names and values are escaped, so a configured value cannot inject
// arbitrary PromQL into every query the builders produce. Keys are sorted so the
// generated query is stable across calls (Go map order is randomised).
func ParsePrometheusAdditionalLabels(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	var obj map[string]string
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", fmt.Errorf("must be a JSON object of string values, e.g. {\"cluster\": \"prod\"}")
	}

	names := make([]string, 0, len(obj))
	for name := range obj {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		if !promqlLabelNameRe.MatchString(name) {
			return "", fmt.Errorf("invalid label name %q", name)
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, name, escapePromQLValue(obj[name])))
	}
	return strings.Join(parts, ","), nil
}

// ExpandClusterPlaceholder substitutes the query builders' __CLUSTER__ token,
// mirroring the relay-server: the matcher fragment gains a trailing separator so
// the templates (which write __CLUSTER__ with no comma after it) stay valid, and
// an unconfigured integration drops the token entirely rather than shipping it to
// the backend.
func ExpandClusterPlaceholder(query, additionalLabels string) string {
	replacement := ""
	if additionalLabels != "" {
		replacement = additionalLabels + " , "
	}
	return strings.ReplaceAll(query, ClusterPlaceholder, replacement)
}

// ClusterPlaceholder is the token the api-server query builders emit.
const ClusterPlaceholder = "__CLUSTER__"

func splitHeaderList(line string) ([]string, bool) {
	if !strings.Contains(line, ",") {
		return nil, false
	}
	parts := strings.Split(line, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		name, _, found := strings.Cut(part, ":")
		if !found || strings.TrimSpace(name) == "" {
			return nil, false
		}
		out = append(out, part)
	}
	return out, true
}

// NewPrometheusUserConfig builds a config from a flat name→value map of already
// decrypted config values. Shared by the save-time validation path and the
// query-time loader so both authenticate identically.
func NewPrometheusUserConfig(values map[string]string) (PrometheusUserConfig, error) {
	cfg := PrometheusUserConfig{
		URL:                normalizePrometheusURL(values[PrometheusURLKey]),
		AuthType:           strings.TrimSpace(values[PrometheusAuthTypeKey]),
		Username:           strings.TrimSpace(values[PrometheusUsernameKey]),
		Password:           values[PrometheusPasswordKey],
		BearerToken:        strings.TrimSpace(values[PrometheusBearerTokenKey]),
		AWSAccessKeyID:     strings.TrimSpace(values[PrometheusAWSAccessKeyIDKey]),
		AWSSecretAccessKey: strings.TrimSpace(values[PrometheusAWSSecretAccessKeyKey]),
		AWSRegion:          strings.TrimSpace(values[PrometheusAWSRegionKey]),
		AWSService:         strings.TrimSpace(values[PrometheusAWSServiceNameKey]),
		CoralogixToken:     strings.TrimSpace(values[PrometheusCoralogixTokenKey]),
		AzureClientID:      strings.TrimSpace(values[PrometheusAzureClientIDKey]),
		AzureClientSecret:  strings.TrimSpace(values[PrometheusAzureClientSecretKey]),
		AzureTenantID:      strings.TrimSpace(values[PrometheusAzureTenantIDKey]),
		AzureResource:      strings.TrimSpace(values[PrometheusAzureResourceKey]),
		RulerType:          strings.TrimSpace(values[PrometheusRulerTypeKey]),
		RulerURL:           normalizePrometheusURL(values[PrometheusRulerURLKey]),
	}
	if cfg.AuthType == "" {
		cfg.AuthType = PrometheusAuthNone
	}
	if cfg.RulerType == "" {
		cfg.RulerType = PrometheusRulerNone
	}
	if cfg.RulerURL == "" {
		cfg.RulerURL = cfg.URL
	}
	if cfg.AWSService == "" {
		cfg.AWSService = PrometheusDefaultAWSService
	}
	if cfg.AzureResource == "" {
		cfg.AzureResource = PrometheusDefaultAzureResource
	}

	headers, err := ParsePrometheusHeaders(values[PrometheusExtraHeadersKey])
	if err != nil {
		return cfg, err
	}
	cfg.ExtraHeaders = headers

	labels, err := ParsePrometheusAdditionalLabels(values[PrometheusAdditionalLabelsKey])
	if err != nil {
		return cfg, err
	}
	cfg.AdditionalLabels = labels
	return cfg, nil
}

// HasRuler reports whether the operator declared a writable ruler API.
func (c PrometheusUserConfig) HasRuler() bool {
	return c.RulerType != "" && c.RulerType != PrometheusRulerNone
}

// Ruler returns the same connection — auth, headers, everything — addressed at
// the ruler base URL, so ruler calls go through Do exactly like queries.
func (c PrometheusUserConfig) Ruler() PrometheusUserConfig {
	ruler := c
	if c.RulerURL != "" {
		ruler.URL = c.RulerURL
	}
	return ruler
}

// normalizePrometheusURL trims whitespace and a trailing slash. Unlike the
// SigNoz and Chronosphere endpoints, a PATH IS PRESERVED: Amazon Managed
// Prometheus serves its API under /workspaces/<id>, and Thanos, Mimir and
// Grafana Cloud are routinely exposed behind an ingress prefix. Stripping the
// path here would break those deployments.
func normalizePrometheusURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// DoGet issues an authenticated GET against the Prometheus HTTP API. apiPath is
// appended to the configured base URL (e.g. "/api/v1/query").
func (c PrometheusUserConfig) DoGet(ctx context.Context, apiPath string, params neturl.Values) (*http.Response, error) {
	return c.Do(ctx, http.MethodGet, apiPath, params, "", nil)
}

// Do issues an authenticated request against the configured base URL. Ruler
// writes (POST / DELETE with a YAML body) come through here; DoGet is the
// body-less special case every metric query uses. The body is hashed for SigV4
// because the empty-payload hash only verifies for a body-less request.
func (c PrometheusUserConfig) Do(ctx context.Context, method, apiPath string, params neturl.Values, contentType string, body []byte) (*http.Response, error) {
	if c.URL == "" {
		return nil, fmt.Errorf("prometheus: base URL not configured")
	}
	target := c.URL + apiPath
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	// Background and test RequestContexts carry no context; http rejects a nil one.
	if ctx == nil {
		ctx = context.Background()
	}

	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, fmt.Errorf("prometheus: failed to build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, values := range c.ExtraHeaders {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	// Auth is applied AFTER the extra headers so SigV4 signs the final header
	// set — the same ordering the agent uses. Reordering these silently breaks
	// AMP whenever an operator also configures extra headers.
	if err := c.applyAuth(ctx, req, payloadHash(body)); err != nil {
		return nil, err
	}

	resp, err := prometheusHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: request to %s failed: %w", apiPath, err)
	}
	return resp, nil
}

// payloadHash is the SigV4 hashed payload for a request body.
func payloadHash(body []byte) string {
	if len(body) == 0 {
		return emptyPayloadHash
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (c PrometheusUserConfig) applyAuth(ctx context.Context, req *http.Request, payloadHash string) error {
	switch c.AuthType {
	case "", PrometheusAuthNone:
		return nil
	case PrometheusAuthBasic:
		req.SetBasicAuth(c.Username, c.Password)
		return nil
	case PrometheusAuthBearerToken:
		req.Header.Set("Authorization", "Bearer "+c.BearerToken)
		return nil
	case PrometheusAuthCoralogix:
		// Coralogix expects the token in a bare `token` header, not Authorization.
		req.Header.Set("token", c.CoralogixToken)
		return nil
	case PrometheusAuthAWSSigV4:
		service := c.AWSService
		if service == "" {
			service = PrometheusDefaultAWSService
		}
		creds := aws.Credentials{AccessKeyID: c.AWSAccessKeyID, SecretAccessKey: c.AWSSecretAccessKey}
		if err := v4.NewSigner().SignHTTP(ctx, creds, req, payloadHash, service, c.AWSRegion, time.Now()); err != nil {
			return fmt.Errorf("prometheus: failed to sign request with SigV4: %w", err)
		}
		return nil
	case PrometheusAuthAzureAD:
		token, err := c.azureBearer(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	default:
		return fmt.Errorf("prometheus: unsupported auth_type %q", c.AuthType)
	}
}

type azureTokenEntry struct {
	token  string
	expiry time.Time
}

// azureTokenRefreshSkew is how far before real expiry a cached token stops being
// reused, so a token cannot expire mid-flight on the request it was fetched for.
const azureTokenRefreshSkew = 60 * time.Second

var (
	azureTokenMu    sync.Mutex
	azureTokenCache = map[string]azureTokenEntry{}
)

// azureBearer returns a cached Azure AD access token, minting a new one when the
// cached token is missing or within a minute of expiring. The cache key includes
// every credential that determines the token, so rotating a client secret cannot
// serve a token minted from the old one.
func (c PrometheusUserConfig) azureBearer(ctx context.Context) (string, error) {
	key := strings.Join([]string{c.AzureTenantID, c.AzureClientID, c.AzureClientSecret, c.AzureResource}, "|")

	if token, ok := cachedAzureToken(key); ok {
		return token, nil
	}

	// Minting runs WITHOUT the lock. azureTokenMu is process-wide, so holding it
	// across the Azure AD round trip would serialise every tenant's authentication
	// behind one slow response — a hang for one integration would stall them all.
	// Concurrent misses may each mint a token: wasteful, but correct, and the
	// double-check below makes them converge on a single cache entry.
	token, expiry, err := c.mintAzureToken(ctx)
	if err != nil {
		return "", err
	}

	azureTokenMu.Lock()
	defer azureTokenMu.Unlock()
	// Another goroutine may have cached a still-valid token while this one minted;
	// prefer that one rather than overwriting it.
	if entry, ok := azureTokenCache[key]; ok && entry.token != "" && azureTokenUsable(entry) {
		return entry.token, nil
	}
	azureTokenCache[key] = azureTokenEntry{token: token, expiry: expiry}
	return token, nil
}

// cachedAzureToken returns a cached token that is still comfortably valid. Split
// out so the read holds the lock for a map lookup and nothing else.
func cachedAzureToken(key string) (string, bool) {
	azureTokenMu.Lock()
	defer azureTokenMu.Unlock()
	if entry, ok := azureTokenCache[key]; ok && entry.token != "" && azureTokenUsable(entry) {
		return entry.token, true
	}
	return "", false
}

// azureTokenUsable reports whether a cached entry has enough life left to be worth
// reusing. Callers must hold azureTokenMu.
func azureTokenUsable(entry azureTokenEntry) bool {
	return time.Now().Add(azureTokenRefreshSkew).Before(entry.expiry)
}

func (c PrometheusUserConfig) mintAzureToken(ctx context.Context) (string, time.Time, error) {
	if c.AzureClientID == "" || c.AzureTenantID == "" || c.AzureClientSecret == "" {
		return "", time.Time{}, fmt.Errorf("prometheus: azure_client_id, azure_tenant_id and azure_client_secret are required for azure_ad auth")
	}

	form := neturl.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.AzureClientID)
	form.Set("client_secret", c.AzureClientSecret)
	form.Set("resource", c.AzureResource)

	endpoint := strings.TrimRight(azureTokenEndpointBase, "/") + "/" + c.AzureTenantID + "/oauth2/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("prometheus: failed to build azure token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := prometheusHTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("prometheus: azure token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("prometheus: failed to read azure token response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", time.Time{}, fmt.Errorf("prometheus: azure token request returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	// expires_in/expires_on come back as JSON strings from some Azure endpoints
	// and as numbers from others, so both are decoded as json.Number.
	var tokenResponse struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
		ExpiresOn   json.Number `json:"expires_on"`
	}
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", time.Time{}, fmt.Errorf("prometheus: failed to decode azure token response: %w", err)
	}
	if tokenResponse.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("prometheus: azure token response contained no access_token")
	}
	return tokenResponse.AccessToken, azureTokenExpiry(tokenResponse.ExpiresOn, tokenResponse.ExpiresIn), nil
}

// azureTokenExpiry prefers the absolute expires_on and falls back to
// now+expires_in. When neither parses, a short expiry keeps a token from being
// cached indefinitely.
func azureTokenExpiry(expiresOn, expiresIn json.Number) time.Time {
	if secs, err := expiresOn.Int64(); err == nil && secs > 0 {
		return time.Unix(secs, 0)
	}
	if secs, err := expiresIn.Int64(); err == nil && secs > 0 {
		return time.Now().Add(time.Duration(secs) * time.Second)
	}
	return time.Now().Add(5 * time.Minute)
}
