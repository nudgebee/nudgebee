package integrations

import (
	"strconv"
	"testing"
	"time"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool { return &b }

func TestMsTeams_ImplementsUserLister(t *testing.T) {
	var _ core.UserLister = MsTeams{}

	intg, found := core.GetIntegration(IntegrationMsTeams)
	assert.True(t, found, "ms_teams must be registered")
	_, ok := intg.(core.UserLister)
	assert.True(t, ok, "ms_teams must implement core.UserLister")
}

func TestMapGraphUser(t *testing.T) {
	t.Run("user with mail", func(t *testing.T) {
		eu, ok := mapGraphUser(msGraphUser{ID: "g1", DisplayName: "John Doe", Mail: "jdoe@example.com", UserPrincipalName: "jdoe@example.com", AccountEnabled: boolPtr(true)})
		assert.True(t, ok)
		assert.Equal(t, core.ExternalUser{ID: "g1", Username: "jdoe@example.com", Email: "jdoe@example.com", DisplayName: "John Doe"}, eu)
	})

	t.Run("falls back to UPN when mail empty", func(t *testing.T) {
		eu, ok := mapGraphUser(msGraphUser{ID: "g2", UserPrincipalName: "svc@contoso.onmicrosoft.com", AccountEnabled: boolPtr(true)})
		assert.True(t, ok)
		assert.Equal(t, "svc@contoso.onmicrosoft.com", eu.Email)
	})

	t.Run("skips disabled and id-less", func(t *testing.T) {
		_, ok := mapGraphUser(msGraphUser{ID: "g3", Mail: "x@y.com", AccountEnabled: boolPtr(false)})
		assert.False(t, ok)
		_, ok = mapGraphUser(msGraphUser{ID: "", Mail: "x@y.com", AccountEnabled: boolPtr(true)})
		assert.False(t, ok)
	})

	t.Run("missing accountEnabled is treated as enabled", func(t *testing.T) {
		eu, ok := mapGraphUser(msGraphUser{ID: "g4", Mail: "a@b.com"})
		assert.True(t, ok)
		assert.Equal(t, "g4", eu.ID)
	})
}

func TestMsTeamsTokenExpired(t *testing.T) {
	assert.False(t, msTeamsTokenExpired(""), "blank => not expired")
	assert.False(t, msTeamsTokenExpired("garbage"), "unparseable => not expired")
	assert.True(t, msTeamsTokenExpired(time.Now().Add(-time.Hour).Format(time.RFC3339)), "past RFC3339 => expired")
	assert.False(t, msTeamsTokenExpired(time.Now().Add(time.Hour).Format(time.RFC3339)), "future RFC3339 => valid")
	assert.True(t, msTeamsTokenExpired(strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)), "past epoch => expired")
}
