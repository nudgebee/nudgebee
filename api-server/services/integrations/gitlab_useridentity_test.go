package integrations

import (
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestGitlab_ImplementsUserLister(t *testing.T) {
	var _ core.UserLister = Gitlab{}

	intg, found := core.GetIntegration(IntegrationGitlab)
	assert.True(t, found, "gitlab must be registered")
	_, ok := intg.(core.UserLister)
	assert.True(t, ok, "gitlab must implement core.UserLister")
}

func TestMapGitlabUser(t *testing.T) {
	t.Run("admin token with email auto-matches", func(t *testing.T) {
		eu, ok := mapGitlabUser(&gitlab.User{ID: 42, Username: "jdoe", Email: "jdoe@example.com", Name: "John Doe"})
		assert.True(t, ok)
		assert.Equal(t, core.ExternalUser{ID: "42", Username: "jdoe", Email: "jdoe@example.com", DisplayName: "John Doe"}, eu)
	})

	t.Run("falls back to public email", func(t *testing.T) {
		eu, ok := mapGitlabUser(&gitlab.User{ID: 7, Username: "p", PublicEmail: "pub@example.com", Name: "Pub"})
		assert.True(t, ok)
		assert.Equal(t, "pub@example.com", eu.Email)
	})

	t.Run("no email stays login-only", func(t *testing.T) {
		eu, ok := mapGitlabUser(&gitlab.User{ID: 9, Username: "noemail", Name: "No Email"})
		assert.True(t, ok)
		assert.Equal(t, "", eu.Email)
		assert.Equal(t, "9", eu.ID)
	})

	t.Run("skips bots and invalid rows", func(t *testing.T) {
		_, ok := mapGitlabUser(&gitlab.User{ID: 5, Username: "bot", Bot: true})
		assert.False(t, ok)
		_, ok = mapGitlabUser(&gitlab.User{ID: 0, Username: "noid"})
		assert.False(t, ok)
		_, ok = mapGitlabUser(nil)
		assert.False(t, ok)
	})
}

func TestMapGitlabMember(t *testing.T) {
	t.Run("group member with email", func(t *testing.T) {
		eu, ok := mapGitlabMember(&gitlab.GroupMember{ID: 11, Username: "jdoe", Email: "jdoe@example.com", Name: "John Doe"})
		assert.True(t, ok)
		assert.Equal(t, core.ExternalUser{ID: "11", Username: "jdoe", Email: "jdoe@example.com", DisplayName: "John Doe"}, eu)
	})

	t.Run("falls back to public email", func(t *testing.T) {
		eu, ok := mapGitlabMember(&gitlab.GroupMember{ID: 12, Username: "p", PublicEmail: "pub@example.com", Name: "Pub"})
		assert.True(t, ok)
		assert.Equal(t, "pub@example.com", eu.Email)
	})

	t.Run("no email stays login-only; skips id-less/nil", func(t *testing.T) {
		eu, ok := mapGitlabMember(&gitlab.GroupMember{ID: 13, Username: "noemail", Name: "No Email"})
		assert.True(t, ok)
		assert.Equal(t, "", eu.Email)
		_, ok = mapGitlabMember(&gitlab.GroupMember{ID: 0})
		assert.False(t, ok)
		_, ok = mapGitlabMember(nil)
		assert.False(t, ok)
	})
}

func TestGitlabGroupsFromProjects(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single project", `[{"name":"nudgebee-group / nudgebee-project","key":"nudgebee-group/nudgebee-project"}]`, []string{"nudgebee-group"}},
		{"nested subgroup", `[{"key":"acme/platform/api"}]`, []string{"acme/platform"}},
		{"distinct groups deduped", `[{"key":"g1/p1"},{"key":"g1/p2"},{"key":"g2/p3"}]`, []string{"g1", "g2"}},
		{"user-namespace project skipped", `[{"key":"justaproject"}]`, nil},
		{"blank/null/garbage", ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, gitlabGroupsFromProjects(tt.in))
		})
	}
	assert.Nil(t, gitlabGroupsFromProjects("null"))
	assert.Nil(t, gitlabGroupsFromProjects("not json"))
}
