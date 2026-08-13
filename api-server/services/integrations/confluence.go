package integrations

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(Confluence{})
}

const IntegrationConfluence = "confluence"

// Confluence auth types. The type determines both the credential shape and the
// REST API base path, because the two are not independent in practice: Cloud
// serves the API under /wiki and has no personal access tokens, while Data
// Center serves it at the instance root and issues PATs as bearer tokens.
const (
	ConfluenceAuthCloudAPIToken      = "cloud_api_token"
	ConfluenceAuthDataCenterPAT      = "datacenter_pat"
	ConfluenceAuthDataCenterPassword = "datacenter_password"
)

// confluenceMaxProbeBytes caps how much of a connection-test response we read.
// A misrouted request can return an arbitrarily large SSO login page.
const confluenceMaxProbeBytes = 64 * 1024

type Confluence struct {
}

func (m Confluence) Name() string {
	return IntegrationConfluence
}

func (m Confluence) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDocs
}

// confluenceAuthType normalises the stored auth_type. Integrations created
// before Data Center support have no auth_type row at all, so an empty value
// must resolve to Cloud + Basic — that is exactly what they do today.
func confluenceAuthType(v string) string {
	switch strings.TrimSpace(v) {
	case ConfluenceAuthDataCenterPAT:
		return ConfluenceAuthDataCenterPAT
	case ConfluenceAuthDataCenterPassword:
		return ConfluenceAuthDataCenterPassword
	default:
		return ConfluenceAuthCloudAPIToken
	}
}

func confluenceIsCloud(mode string) bool {
	return confluenceAuthType(mode) == ConfluenceAuthCloudAPIToken
}

// confluenceNeedsUsername reports whether the auth type authenticates with a
// username. Personal access tokens carry their own identity.
func confluenceNeedsUsername(mode string) bool {
	return confluenceAuthType(mode) != ConfluenceAuthDataCenterPAT
}

func confluenceAuthLabel(mode string) string {
	switch confluenceAuthType(mode) {
	case ConfluenceAuthDataCenterPAT:
		return "Data Center / Server (personal access token)"
	case ConfluenceAuthDataCenterPassword:
		return "Data Center / Server (username + password)"
	default:
		return "Confluence Cloud (email + API token)"
	}
}

// confluenceAPIBase returns the REST API root for a host. Cloud serves it under
// /wiki; Data Center serves it directly beneath whatever context path the
// instance is deployed at (commonly /confluence), so the host is used verbatim.
func confluenceAPIBase(host, mode string) string {
	base := strings.TrimRight(strings.TrimSpace(host), "/")
	if !confluenceIsCloud(mode) {
		return base + "/rest/api"
	}
	// Tolerate a Cloud host pasted with the /wiki suffix already on it.
	base = strings.TrimSuffix(base, "/wiki")
	return base + "/wiki/rest/api"
}

// confluenceAuthHeader builds the Authorization header value. Data Center
// personal access tokens are bearer tokens; every other type is HTTP Basic.
func confluenceAuthHeader(mode, username, token string) string {
	if confluenceAuthType(mode) == ConfluenceAuthDataCenterPAT {
		return "Bearer " + token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+token))
}

func (m Confluence) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{"token", "host"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"auth_type": {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication method",
				Default:     ConfluenceAuthCloudAPIToken,
				Enum: []any{
					ConfluenceAuthCloudAPIToken,
					ConfluenceAuthDataCenterPAT,
					ConfluenceAuthDataCenterPassword,
				},
				IsTestable: true,
				Priority:   82,
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence base URL. Include the context path if your Data Center instance uses one (e.g. https://wiki.example.com/confluence)",
				Default:     "",
				IsTestable:  true,
				Priority:    80,
			},
			"username": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Atlassian account email (Cloud) or Confluence username (Data Center)",
				Default:      "",
				IsTestable:   true,
				ShowWhen:     map[string]any{"auth_type": []any{ConfluenceAuthCloudAPIToken, ConfluenceAuthDataCenterPassword}},
				RequiredWhen: map[string]any{"auth_type": []any{ConfluenceAuthCloudAPIToken, ConfluenceAuthDataCenterPassword}},
				Priority:     70,
			},
			"token": {
				Type:        core.ToolSchemaTypeString,
				Description: "API token (Cloud), personal access token, or password — whichever the selected authentication method uses",
				Default:     "",
				IsTestable:  true,
				IsEncrypted: true,
				Priority:    68,
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Confluence namespace",
				Default:     "",
				Priority:    50,
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Confluence Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
		},
	}
}

func (m Confluence) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range integrationConfig {
		configMap[c.Name] = c.Value
	}

	mode := confluenceAuthType(configMap["auth_type"])
	host := strings.TrimSpace(configMap["host"])
	username := strings.TrimSpace(configMap["username"])
	token := configMap["token"]

	if host == "" {
		return []error{fmt.Errorf("host is required")}
	}
	if token == "" {
		return []error{fmt.Errorf("a credential is required for %s", confluenceAuthLabel(mode))}
	}
	if confluenceNeedsUsername(mode) && username == "" {
		return []error{fmt.Errorf("username is required for %s", confluenceAuthLabel(mode))}
	}

	if err := confluenceValidateHost(host, mode); err != nil {
		return []error{err}
	}

	authHeader := confluenceAuthHeader(mode, username, token)
	apiBase := confluenceAPIBase(host, mode)

	result, err := confluenceProbeSpaces(apiBase, authHeader, map[string]string{"limit": "1"})
	if err != nil {
		return []error{confluenceDiagnoseTransport(host, err)}
	}
	if err := confluenceInterpretProbe(result, host, mode, authHeader); err != nil {
		return []error{err}
	}

	// namespace is the Confluence space key the RAG scraper walks. When set it must
	// resolve to a real, accessible space — otherwise the connection test would report
	// success for a space that yields zero pages. An empty namespace means "all spaces"
	// and is left unvalidated by design.
	namespace := strings.TrimSpace(configMap["namespace"])
	if namespace == "" {
		return nil
	}
	return confluenceValidateNamespace(apiBase, authHeader, namespace)
}

// confluenceValidateHost rejects malformed URLs, and URLs whose shape
// contradicts the selected mode. Catching these before any network call turns
// the most common misconfiguration into an immediate, specific message rather
// than a 404 several seconds later.
func confluenceValidateHost(host, mode string) error {
	parsed, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		example := "https://wiki.example.com"
		if confluenceIsCloud(mode) {
			example = "https://your-domain.atlassian.net"
		}
		return fmt.Errorf("host must be a valid URL with an http or https scheme (e.g. %s)", example)
	}

	isAtlassianHosted := strings.HasSuffix(parsed.Hostname(), ".atlassian.net") || strings.HasSuffix(parsed.Hostname(), ".jira.com")
	if isAtlassianHosted && !confluenceIsCloud(mode) {
		return fmt.Errorf("%s is an Atlassian-hosted Cloud site, but the authentication method is set to %s. Cloud sites do not issue personal access tokens — select the Cloud method and use an API token", parsed.Hostname(), confluenceAuthLabel(mode))
	}
	if strings.Contains(parsed.Path, "/rest/api") {
		return fmt.Errorf("host must be the Confluence base URL, not an API endpoint — remove the /rest/api path")
	}
	return nil
}

type confluenceProbeResult struct {
	statusCode  int
	contentType string
	body        []byte
}

func confluenceProbeSpaces(apiBase, authHeader string, params map[string]string) (*confluenceProbeResult, error) {
	resp, err := common.HttpGet(
		apiBase+"/space",
		common.HttpWithHeaders(map[string]string{
			"Authorization": authHeader,
			"Accept":        "application/json",
		}),
		common.HttpWithQueryParams(params),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, confluenceMaxProbeBytes))
	if err != nil {
		return nil, err
	}
	return &confluenceProbeResult{
		statusCode:  resp.StatusCode,
		contentType: resp.Header.Get("Content-Type"),
		body:        body,
	}, nil
}

// confluenceDiagnoseTransport names the cause of a failure that happened before
// any HTTP status was returned. These are the failures a Data Center customer
// hits and we cannot see: their DNS, their certificate authority, their
// firewall. The message has to be actionable on their side, because they are
// the only ones who can act on it.
func confluenceDiagnoseTransport(host string, err error) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf("cannot resolve the hostname in %q from NudgeBee. If this is an internal Data Center instance, it must be reachable from where NudgeBee runs", host)
	}

	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	if errors.As(err, &certErr) || errors.As(err, &unknownAuthority) {
		return fmt.Errorf("the TLS certificate presented by %q is not trusted. On-premise instances commonly use an internal certificate authority, which NudgeBee does not currently trust", host)
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return fmt.Errorf("the TLS certificate presented by %q does not cover that hostname", host)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("timed out connecting to %q. Check that the instance is reachable from NudgeBee and that no firewall is dropping the connection", host)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return fmt.Errorf("cannot connect to %q: %v. Check the scheme, port, and any firewall between NudgeBee and the instance", host, opErr.Err)
	}
	return fmt.Errorf("failed to connect to Confluence at %q: %w", host, err)
}

// confluenceInterpretProbe turns the connection-test response into a message
// that names what to change. The previous implementation collapsed everything
// here into a bare status code, which is unactionable for an operator who can
// see neither our logs nor our request.
func confluenceInterpretProbe(result *confluenceProbeResult, host, mode, authHeader string) error {
	// A non-JSON body means something other than Confluence answered — typically
	// an SSO portal or a reverse proxy that intercepted the request. This
	// otherwise surfaces as an inscrutable parse failure.
	if result.statusCode == http.StatusOK && !confluenceLooksJSON(result.contentType, result.body) {
		return fmt.Errorf("%q returned a non-JSON response to an API request, which usually means an SSO portal or reverse proxy intercepted it before it reached Confluence. The REST API must be reachable without an interactive login", host)
	}

	switch result.statusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return confluenceUnauthorizedError(mode)
	case http.StatusForbidden:
		return fmt.Errorf("the credentials are valid but lack permission to list spaces (HTTP 403). Grant the account read access to at least one space")
	case http.StatusNotFound:
		return confluenceNotFoundError(host, mode, authHeader)
	default:
		return fmt.Errorf("Confluence at %q returned HTTP %d for %s", host, result.statusCode, confluenceAPIBase(host, mode)+"/space")
	}
}

func confluenceUnauthorizedError(mode string) error {
	switch confluenceAuthType(mode) {
	case ConfluenceAuthDataCenterPAT:
		return fmt.Errorf("Confluence rejected the personal access token (HTTP 401). The token may be expired or revoked, or personal access tokens may be disabled on the instance")
	case ConfluenceAuthDataCenterPassword:
		return fmt.Errorf("Confluence rejected the username and password (HTTP 401). If the instance authenticates through SSO, basic authentication is often disabled — use a personal access token instead")
	default:
		return fmt.Errorf("Confluence rejected the email and API token (HTTP 401). Cloud requires an API token from id.atlassian.com, not an account password")
	}
}

// confluenceNotFoundError re-probes the other deployment's base path. A 404 on
// ours and anything else on theirs is the signature of the wrong deployment
// type, which is the single most likely misconfiguration — so name the fix
// instead of reporting the status code.
func confluenceNotFoundError(host, mode, authHeader string) error {
	alternate := ConfluenceAuthDataCenterPAT
	suggestion := "Data Center / Server"
	if !confluenceIsCloud(mode) {
		alternate = ConfluenceAuthCloudAPIToken
		suggestion = "Confluence Cloud"
	}

	if result, err := confluenceProbeSpaces(confluenceAPIBase(host, alternate), authHeader, map[string]string{"limit": "1"}); err == nil && result.statusCode != http.StatusNotFound {
		return fmt.Errorf("the REST API was not found at %s, but it does respond at %s. This host looks like %s — change the authentication method to match", confluenceAPIBase(host, mode), confluenceAPIBase(host, alternate), suggestion)
	}

	return fmt.Errorf("the Confluence REST API was not found at %s (HTTP 404). Check the base URL, including the context path if the instance is served under one (e.g. https://wiki.example.com/confluence)", confluenceAPIBase(host, mode))
}

func confluenceLooksJSON(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "json") {
		return true
	}
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// confluenceValidateNamespace confirms a configured space key resolves.
//
// The space-key-filtered list endpoint returns HTTP 200 with an EMPTY results
// list for an unknown or inaccessible key rather than a 404, so the response
// body must be inspected to decide presence.
func confluenceValidateNamespace(apiBase, authHeader, namespace string) []error {
	result, err := confluenceProbeSpaces(apiBase, authHeader, map[string]string{
		"spaceKey": namespace,
		"limit":    "1",
	})
	if err != nil {
		return []error{fmt.Errorf("failed to verify Confluence space %q: %w", namespace, err)}
	}
	if result.statusCode != http.StatusOK {
		return []error{fmt.Errorf("failed to verify Confluence space %q: Confluence returned HTTP %d", namespace, result.statusCode)}
	}

	var spaceList struct {
		Results []struct {
			Key string `json:"key"`
		} `json:"results"`
	}
	if err := json.Unmarshal(result.body, &spaceList); err != nil {
		return []error{fmt.Errorf("failed to parse the Confluence space response for %q: %w", namespace, err)}
	}

	for _, s := range spaceList.Results {
		if strings.EqualFold(s.Key, namespace) {
			return nil
		}
	}
	return []error{fmt.Errorf("Confluence space %q does not exist or is not accessible to these credentials", namespace)}
}
