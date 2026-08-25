package core

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ctxWithDisplayName builds a RequestContext whose security context carries the
// given display name (set via the JSON wire, the only way the private field is
// populated in prod — over the wire from api-server).
func ctxWithDisplayName(t *testing.T, displayName string) *security.RequestContext {
	t.Helper()
	sc := &security.SecurityContext{}
	require.NoError(t, json.Unmarshal([]byte(`{"DisplayName":`+strconv.Quote(displayName)+`}`), sc))
	return security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)
}

func TestRenderUserContextBlock(t *testing.T) {
	// Full name → first token only, wrapped in the tag the base prompt reads.
	assert.Equal(t,
		"<user_context>The user you are assisting is Piyush.</user_context>",
		renderUserContextBlock(ctxWithDisplayName(t, "Piyush Bhavsar")))

	// Single-token name works too.
	assert.Equal(t,
		"<user_context>The user you are assisting is Piyush.</user_context>",
		renderUserContextBlock(ctxWithDisplayName(t, "Piyush")))

	// Blank / whitespace display name → no block (generic greeting).
	assert.Equal(t, "", renderUserContextBlock(ctxWithDisplayName(t, "   ")))

	// Nil security context → no block, no panic.
	nilCtx := security.NewRequestContext(context.Background(), nil, slog.Default(), nil, nil)
	assert.Equal(t, "", renderUserContextBlock(nilCtx))
}
