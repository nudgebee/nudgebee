package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andygrunwald/go-jira"
	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func TestJira_ImplementsUserLister(t *testing.T) {
	var _ core.UserLister = Jira{}

	intg, found := core.GetIntegration(IntegrationJira)
	assert.True(t, found, "jira must be registered")
	_, ok := intg.(core.UserLister)
	assert.True(t, ok, "jira must implement core.UserLister")
}

func TestMapJiraUser(t *testing.T) {
	t.Run("cloud user with email", func(t *testing.T) {
		eu, ok := mapJiraUser(jira.User{AccountID: "5b10a", EmailAddress: "jdoe@example.com", DisplayName: "John Doe", AccountType: "atlassian", Active: true})
		assert.True(t, ok)
		assert.Equal(t, core.ExternalUser{ID: "5b10a", Username: "5b10a", Email: "jdoe@example.com", DisplayName: "John Doe"}, eu)
	})

	t.Run("cloud user without email stays login-only", func(t *testing.T) {
		eu, ok := mapJiraUser(jira.User{AccountID: "5b10b", DisplayName: "No Email", AccountType: "atlassian", Active: true})
		assert.True(t, ok)
		assert.Equal(t, "5b10b", eu.ID)
		assert.Equal(t, "", eu.Email)
	})

	t.Run("server user keyed by name", func(t *testing.T) {
		eu, ok := mapJiraUser(jira.User{Name: "jdoe", Key: "jdoe", EmailAddress: "jdoe@corp.com", DisplayName: "John Doe", Active: true})
		assert.True(t, ok)
		assert.Equal(t, core.ExternalUser{ID: "jdoe", Username: "jdoe", Email: "jdoe@corp.com", DisplayName: "John Doe"}, eu)
	})

	t.Run("skips app, inactive, and id-less rows", func(t *testing.T) {
		_, ok := mapJiraUser(jira.User{AccountID: "app1", AccountType: "app", Active: true})
		assert.False(t, ok)
		_, ok = mapJiraUser(jira.User{AccountID: "5b10c", DisplayName: "Deactivated", AccountType: "atlassian", Active: false})
		assert.False(t, ok)
		_, ok = mapJiraUser(jira.User{DisplayName: "No ID", Active: true})
		assert.False(t, ok)
	})
}

// Cloud lists users through the bulk users/search endpoint; Server/Data Center
// lacks it and is listed through user/search with a wildcard username.
func TestJira_ListUsers_EndpointByDeployment(t *testing.T) {
	for _, tt := range []struct {
		name, deploymentType, wantPath, wantQuery string
	}{
		{"cloud", "Cloud", "/rest/api/2/users/search", ""},
		{"data center", "Server", "/rest/api/2/user/search", "."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			hits := map[string]int{}
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits[r.URL.Path]++
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/rest/api/2/serverInfo":
					_, _ = w.Write([]byte(`{"deploymentType":"` + tt.deploymentType + `"}`))
				case tt.wantPath:
					if got := r.URL.Query().Get("username"); got != tt.wantQuery {
						t.Errorf("username query = %q, want %q", got, tt.wantQuery)
					}
					_, _ = w.Write([]byte(`[{"name":"jdoe","key":"jdoe","emailAddress":"jdoe@corp.com","displayName":"John Doe","active":true}]`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			orig := http.DefaultTransport
			http.DefaultTransport = ts.Client().Transport
			defer func() { http.DefaultTransport = orig }()

			values := []core.IntegrationConfigValue{
				{Name: JiraConfigUrl, Value: strings.TrimPrefix(ts.URL, "https://")},
				{Name: JiraConfigUsername, Value: "u"},
				{Name: JiraConfigPassword, Value: "p"},
			}
			users, err := Jira{}.ListUsers(context.Background(), values)
			assert.NoError(t, err)
			assert.Equal(t, 1, hits[tt.wantPath])
			assert.Len(t, users, 1)
			assert.Equal(t, "jdoe@corp.com", users[0].Email)
		})
	}
}
