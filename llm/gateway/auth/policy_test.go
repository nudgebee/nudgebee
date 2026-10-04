package auth

import (
	"testing"

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
