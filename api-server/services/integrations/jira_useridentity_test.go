package integrations

import (
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
