package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKeyPolicy_ZeroValueIsUnrestricted(t *testing.T) {
	var p KeyPolicy
	assert.False(t, p.Restricted())
	assert.True(t, p.AllowsEndpoint(EndpointEmbeddings))
	assert.True(t, p.AllowsProvider("anthropic"))
}

func TestKeyPolicy_EmptyListAllowsNothing(t *testing.T) {
	// Only a hand edit can store '{}'; it must narrow the key, never widen it.
	p := KeyPolicy{Providers: []string{}}
	assert.True(t, p.Restricted())
	assert.False(t, p.AllowsProvider("openai"))
	assert.True(t, p.AllowsEndpoint(EndpointChat), "other dimensions stay unrestricted")
}

func TestKeyPolicy_UnknownEndpointNameAllowsNothingExtra(t *testing.T) {
	p := KeyPolicy{Endpoints: []string{"chat", "realtime"}}
	assert.True(t, p.AllowsEndpoint(EndpointChat))
	assert.False(t, p.AllowsEndpoint(EndpointResponses))
}

func TestUserAccess_Denial(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	assert.Empty(t, UserAccess{}.Denial(now), "no row = active")
	assert.Empty(t, UserAccess{ValidUntil: &future}.Denial(now))
	assert.Contains(t, UserAccess{Suspended: true}.Denial(now), "suspended")
	assert.Contains(t, UserAccess{ValidUntil: &past}.Denial(now), "ended")
	assert.Contains(t, UserAccess{ValidUntil: &now}.Denial(now), "ended", "the end instant is already over")
	assert.Contains(t, UserAccess{Suspended: true, ValidUntil: &future}.Denial(now), "suspended")
}

func TestIdentity_Restricted(t *testing.T) {
	assert.False(t, Identity{}.Restricted())
	assert.True(t, Identity{UserPolicy: KeyPolicy{Models: []string{"m"}}}.Restricted())
	assert.True(t, Identity{Policy: KeyPolicy{Endpoints: []string{"chat"}}}.Restricted())
}
