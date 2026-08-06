package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfluenceAPIBase(t *testing.T) {
	cases := []struct {
		name string
		host string
		mode string
		want string
	}{
		{"cloud bare host", "https://acme.atlassian.net", confluenceAuthCloudAPIToken, "https://acme.atlassian.net/wiki/rest/api"},
		{"cloud host already carries /wiki", "https://acme.atlassian.net/wiki", confluenceAuthCloudAPIToken, "https://acme.atlassian.net/wiki/rest/api"},
		{"absent mode is cloud", "https://acme.atlassian.net", "", "https://acme.atlassian.net/wiki/rest/api"},
		{"dc pat", "https://wiki.acme.internal", confluenceAuthDataCenterPAT, "https://wiki.acme.internal/rest/api"},
		{"dc basic", "https://wiki.acme.internal", confluenceAuthDataCenterPassword, "https://wiki.acme.internal/rest/api"},
		{"dc context path", "https://wiki.acme.internal/confluence/", confluenceAuthDataCenterPAT, "https://wiki.acme.internal/confluence/rest/api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, confluenceAPIBase(c.host, c.mode))
		})
	}
}

func TestConfluenceSiteBase(t *testing.T) {
	assert.Equal(t, "https://acme.atlassian.net/wiki", confluenceSiteBase("https://acme.atlassian.net", confluenceAuthCloudAPIToken))
	assert.Equal(t, "https://acme.atlassian.net/wiki", confluenceSiteBase("https://acme.atlassian.net/wiki/", confluenceAuthCloudAPIToken))
	assert.Equal(t, "https://wiki.acme.internal", confluenceSiteBase("https://wiki.acme.internal", confluenceAuthDataCenterPAT))
	assert.Equal(t, "https://wiki.acme.internal/confluence", confluenceSiteBase("https://wiki.acme.internal/confluence", confluenceAuthDataCenterPassword))
}

func TestConfluenceAuthHeader(t *testing.T) {
	const wantBasic = "Basic bWVAYWNtZS5jb206dG9r" // base64("me@acme.com:tok")

	assert.Equal(t, wantBasic, confluenceAuthHeader(confluenceAuthCloudAPIToken, "me@acme.com", "tok"))
	assert.Equal(t, wantBasic, confluenceAuthHeader(confluenceAuthDataCenterPassword, "me@acme.com", "tok"))
	assert.Equal(t, wantBasic, confluenceAuthHeader("", "me@acme.com", "tok"))
	assert.Equal(t, "Bearer tok", confluenceAuthHeader(confluenceAuthDataCenterPAT, "", "tok"))
}

func TestConfluencePageURL(t *testing.T) {
	// Confluence reports its own base URL, which already accounts for Cloud's
	// /wiki prefix and any Data Center context path.
	assert.Equal(t,
		"https://acme.atlassian.net/wiki/spaces/ENG/pages/123/Runbook",
		confluencePageURL("https://acme.atlassian.net/wiki", "/spaces/ENG/pages/123/Runbook", "https://ignored"))

	assert.Equal(t,
		"https://wiki.acme.internal/confluence/display/ENG/Runbook",
		confluencePageURL("https://wiki.acme.internal/confluence/", "/display/ENG/Runbook", "https://ignored"))

	// Falls back only when the response omits _links.base.
	assert.Equal(t,
		"https://wiki.acme.internal/display/ENG/Runbook",
		confluencePageURL("", "/display/ENG/Runbook", "https://wiki.acme.internal/"))

	assert.Equal(t, "", confluencePageURL("https://acme.atlassian.net/wiki", "", "https://fallback"))
}

func TestConfluenceConfigComplete(t *testing.T) {
	// A personal access token carries its own identity, so requiring a username
	// would reject every Data Center PAT configuration.
	assert.True(t, confluenceConfigComplete(map[string]string{
		"auth_type": confluenceAuthDataCenterPAT,
		"host":      "https://wiki.acme.internal",
		"token":     "pat",
	}))

	assert.False(t, confluenceConfigComplete(map[string]string{
		"auth_type": confluenceAuthDataCenterPassword,
		"host":      "https://wiki.acme.internal",
		"token":     "pw",
	}))

	// Legacy rows have no auth_type and must keep validating as Cloud + Basic.
	assert.True(t, confluenceConfigComplete(map[string]string{
		"host":     "https://acme.atlassian.net",
		"username": "me@acme.com",
		"token":    "tok",
	}))

	assert.False(t, confluenceConfigComplete(map[string]string{
		"auth_type": confluenceAuthDataCenterPAT,
		"host":      "https://wiki.acme.internal",
	}))
}
