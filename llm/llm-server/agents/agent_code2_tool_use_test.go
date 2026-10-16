package agents

import (
	"errors"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/require"
)

func TestCodeAgent2_ToollessFailureGuard(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	query := core.NBAgentRequest{ConversationId: "tool-less-conversation", MessageId: "first-message"}
	key, ok := codeAgentGuardKey(query.ConversationId, query.MessageId)
	require.True(t, ok)
	defer func() { _ = common.CacheDelete(codeAgentFailuresCacheNS, key) }()

	// This is the error returned by the workspace poller when /status is
	// failed. It must use the existing failure guard, just like a timeout.
	failure := errors.New(`analysis failed: model "tenant-model" executed no repository tools; code analysis could not examine the source`)
	require.False(t, shouldSkipRetryGuard(failure))
	require.NoError(t, common.CacheSet(codeAgentFailuresCacheNS, key, []byte(failure.Error())))

	_, err := (CodeAgent2{}).Execute(ctx, query)
	require.ErrorContains(t, err, "code analysis already attempted in this message")
	require.ErrorContains(t, err, "tenant-model")

	// A new user message gets past the guard and reaches input validation;
	// the empty query stops it there, before any external service is called.
	query.MessageId = "next-message"
	_, err = (CodeAgent2{}).Execute(ctx, query)
	require.ErrorContains(t, err, "query is required")
}
