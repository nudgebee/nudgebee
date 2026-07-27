package proxy

import (
	"testing"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/routing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
)

func TestTierLane(t *testing.T) {
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
