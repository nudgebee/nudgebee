package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// TestHandleConversationTimeAggregatesApi_ForbiddenAccount verifies the
// authorization guard: when the caller requests an account it cannot read,
// the handler returns a "forbidden" error before any DAO/DB access. The
// security context is built in-memory (NewRequestContextForTenantAccountAdmin
// does not touch the DB and HasAccountAccess is an in-memory membership check),
// so this is fully hermetic — it was previously mis-tagged e2e behind a dead
// ctx==nil skip.
func TestHandleConversationTimeAggregatesApi_ForbiddenAccount(t *testing.T) {
	allowedAccount := "00000000-0000-0000-0000-000000000001"
	deniedAccount := "11111111-1111-1111-1111-111111111111"

	ctx := security.NewRequestContextForTenantAccountAdmin(
		"00000000-0000-0000-0000-000000000000",
		"00000000-0000-0000-0000-000000000099",
		[]string{allowedAccount},
	)

	request := ConversationTimeAggregatesRequest{
		AccountId: deniedAccount,
		StartDate: "2026-05-01T00:00:00Z",
		EndDate:   "2026-05-02T00:00:00Z",
	}
	_, err := HandleConversationTimeAggregatesApi(ctx, request)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}
