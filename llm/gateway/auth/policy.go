package auth

import "strings"

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
