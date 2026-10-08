package auth

import (
	"strings"
	"time"
)

// Endpoint families a key policy can allow. The api-server validates against the same
// four names (services/user/token_policy.go); an unknown name in a stored policy allows
// nothing, so a drifted value narrows a key rather than widening it.
const (
	EndpointChat          = "chat"
	EndpointResponses     = "responses"
	EndpointEmbeddings    = "embeddings"
	EndpointConversations = "conversations"
)

// KeyPolicy restricts what one API key may reach. A nil slice leaves that dimension
// unrestricted; the zero value restricts nothing, which is every key minted before #38966.
// Models are matched in proxy (allowsModel), which reads an entry the way /v1 reads a model
// name, so "anthropic/claude-…" pins the provider exactly as it does in a request.
type KeyPolicy struct {
	Providers []string
	Models    []string
	Endpoints []string
}

// Restricted reports whether any dimension is narrowed. A restricted key is refused on
// every path the gateway cannot classify, so an unmodelled endpoint is never a bypass.
func (p KeyPolicy) Restricted() bool {
	return p.Providers != nil || p.Models != nil || p.Endpoints != nil
}

// AllowsEndpoint reports whether the key may call the given endpoint family.
func (p KeyPolicy) AllowsEndpoint(family string) bool {
	return p.Endpoints == nil || contains(p.Endpoints, family)
}

// AllowsProvider reports whether the key may reach the given provider.
func (p KeyPolicy) AllowsProvider(provider string) bool {
	return p.Providers == nil || contains(p.Providers, provider)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// UserAccess is a tenant admin's switch on one user's gateway access, shared by every key the
// user owns. The zero value is on, which is every user without an llm_gateway_user_access row.
type UserAccess struct {
	Suspended  bool
	ValidUntil *time.Time
}

// Denial returns why the user's gateway access is off at now, or "" when it is on.
func (a UserAccess) Denial(now time.Time) string {
	switch {
	case a.Suspended:
		return "Your AI Gateway access is suspended. Ask a tenant admin to restore it."
	case a.ValidUntil != nil && !now.Before(*a.ValidUntil):
		return "Your AI Gateway access ended on " + a.ValidUntil.UTC().Format(time.RFC3339) + ". Ask a tenant admin to extend it."
	}
	return ""
}
