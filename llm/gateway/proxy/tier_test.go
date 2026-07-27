package proxy

import (
	"testing"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/routing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

// withTiers sets the deployment-wide tiering flag for the duration of a test.
func withTiers(t *testing.T, on bool) {
	t.Helper()
	prev := config.Config.TiersEnabled
	config.Config.TiersEnabled = on
	t.Cleanup(func() { config.Config.TiersEnabled = prev })
}

func TestTierLane(t *testing.T) {
	withTiers(t, true)
	h := &handler{router: routing.NewEngine(routing.DefaultTierRules())}

	// A tier alias resolves to its target provider lane.
	lane, ok := h.tierLane(auth.Identity{}, "nb-smart")
	assert.True(t, ok)
	assert.Equal(t, schemas.Anthropic, lane)

	lane, ok = h.tierLane(auth.Identity{}, "nb-fast")
	assert.True(t, ok)
	assert.Equal(t, schemas.Gemini, lane)

	// A non-tier model is not a lane.
	_, ok = h.tierLane(auth.Identity{}, "gpt-5")
	assert.False(t, ok)

	// No router configured → never a tier.
	_, ok = (&handler{}).tierLane(auth.Identity{}, "nb-fast")
	assert.False(t, ok)
}

func TestTierLane_DisabledDeploymentWide(t *testing.T) {
	withTiers(t, false)
	h := &handler{router: routing.NewEngine(routing.DefaultTierRules())}
	// Tiering off → even a valid tier alias is not a lane (falls through to a 400).
	_, ok := h.tierLane(auth.Identity{}, "nb-smart")
	assert.False(t, ok)
}
