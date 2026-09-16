package clients

import (
	"net/http"
	"strings"
	"time"

	jira "github.com/andygrunwald/go-jira"
)

// jiraHTTPTimeout is the timeout for the Jira HTTP client.
const jiraHTTPTimeout = 15 * time.Second

// JiraAuthDataCenterPAT is the auth_type for Jira Data Center personal access
// tokens, which must be sent as a bearer token — Data Center rejects them over
// Basic with 401. Every other auth_type (including "token" and unset, which is
// what existing integrations store) uses Basic. Mirrors the api-server Jira
// integration schema (api-server/services/integrations/jira.go).
const JiraAuthDataCenterPAT = "datacenter_pat"

// IsJiraDataCenterPAT reports whether the stored auth_type is a Data Center PAT.
func IsJiraDataCenterPAT(authType string) bool {
	return strings.TrimSpace(authType) == JiraAuthDataCenterPAT
}

func CreateJiraClient(authType, username, password, url string) (*jira.Client, error) {
	var ct *http.Client
	if IsJiraDataCenterPAT(authType) {
		tp := jira.BearerAuthTransport{Token: password}
		ct = tp.Client()
	} else {
		tp := jira.BasicAuthTransport{
			Username: username,
			Password: password,
		}
		ct = tp.Client()
	}
	ct.Timeout = jiraHTTPTimeout
	client, err := jira.NewClient(ct, "https://"+strings.TrimPrefix(url, "https://"))
	if err != nil {
		return nil, err
	}

	return client, nil
}
