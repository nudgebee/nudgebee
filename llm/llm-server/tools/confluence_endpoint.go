package tools

import (
	"encoding/base64"
	"strings"
)

// Confluence auth types, mirroring the api-server integration schema
// (api-server/services/integrations/confluence.go). The type determines both
// the credential shape and the REST API base path: Cloud serves the API under
// /wiki and authenticates with Basic, while Data Center serves it at the
// instance root and issues personal access tokens as bearer tokens.
//
// api-server and llm-server are separate Go modules, so these helpers are
// duplicated rather than shared. Any change here must be mirrored there.
const (
	confluenceAuthCloudAPIToken      = "cloud_api_token"
	confluenceAuthDataCenterPAT      = "datacenter_pat"
	confluenceAuthDataCenterPassword = "datacenter_password"
)

// confluenceAuthType normalises the stored auth_type. Integrations created
// before Data Center support have no auth_type row, so an empty value must
// resolve to Cloud + Basic — which is what they already do.
func confluenceAuthType(v string) string {
	switch strings.TrimSpace(v) {
	case confluenceAuthDataCenterPAT:
		return confluenceAuthDataCenterPAT
	case confluenceAuthDataCenterPassword:
		return confluenceAuthDataCenterPassword
	default:
		return confluenceAuthCloudAPIToken
	}
}

func confluenceIsCloud(mode string) bool {
	return confluenceAuthType(mode) == confluenceAuthCloudAPIToken
}

// confluenceAPIBase returns the REST API root for a host. Cloud serves it under
// /wiki; Data Center serves it directly beneath whatever context path the
// instance is deployed at, so the host is used verbatim.
func confluenceAPIBase(host, mode string) string {
	base := strings.TrimRight(strings.TrimSpace(host), "/")
	if !confluenceIsCloud(mode) {
		return base + "/rest/api"
	}
	base = strings.TrimSuffix(base, "/wiki")
	return base + "/wiki/rest/api"
}

// confluenceSiteBase is the browser-facing base URL. Only used as a fallback
// when a Confluence response omits _links.base.
func confluenceSiteBase(host, mode string) string {
	base := strings.TrimRight(strings.TrimSpace(host), "/")
	if !confluenceIsCloud(mode) {
		return base
	}
	base = strings.TrimSuffix(base, "/wiki")
	return base + "/wiki"
}

// confluenceAuthHeader builds the Authorization header value. Data Center
// personal access tokens are bearer tokens; every other type is HTTP Basic.
func confluenceAuthHeader(mode, username, token string) string {
	if confluenceAuthType(mode) == confluenceAuthDataCenterPAT {
		return "Bearer " + token
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+token))
}

// confluencePageURL builds an absolute page URL from a Confluence _links block.
// Confluence reports its own base URL in every response, which is already
// correct for Cloud (where it includes /wiki) and for Data Center served under
// a context path — both of which we would otherwise have to reconstruct.
// fallbackBase is used only when the response omits _links.base.
func confluencePageURL(linksBase, webui, fallbackBase string) string {
	if webui == "" {
		return ""
	}
	base := strings.TrimRight(strings.TrimSpace(linksBase), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(fallbackBase), "/")
	}
	return base + webui
}

// confluenceConfigComplete reports whether an integration config carries every
// credential its auth mode needs. Personal access tokens have no username, so
// requiring one would reject every Data Center PAT configuration.
func confluenceConfigComplete(config map[string]string) bool {
	if config["host"] == "" || config["token"] == "" {
		return false
	}
	if confluenceAuthType(config["auth_type"]) == confluenceAuthDataCenterPAT {
		return true
	}
	return config["username"] != ""
}
