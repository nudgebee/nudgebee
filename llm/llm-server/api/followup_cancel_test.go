package api

import (
	"net/http"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleFollowupCancel_DisabledByFlag confirms the feature fails closed
// when FollowupCancelEnabled is off (the default), before any other
// validation runs. (#27582)
func TestHandleFollowupCancel_DisabledByFlag(t *testing.T) {
	origFlag := config.Config.FollowupCancelEnabled
	config.Config.FollowupCancelEnabled = false
	t.Cleanup(func() { config.Config.FollowupCancelEnabled = origFlag })

	ctx := security.NewRequestContextForSuperAdmin()
	resp, status, err := handleFollowupCancel(ctx, ConversationApiRequest{
		ConversationId: "00000000-0000-0000-0000-000000000000",
		Resolution:     "dismiss",
	})
	assert.Nil(t, resp)
	assert.Equal(t, http.StatusNotFound, status)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "not enabled")
	}
}

// Validation paths in handleFollowupCancel must reject bad input BEFORE the
// DAO is consulted — otherwise a malformed client call would hit the DB
// unnecessarily. These tests exercise only the short-circuit branches so
// they can run without a live database. (#27582)
func TestHandleFollowupCancel_Validation(t *testing.T) {
	origFlag := config.Config.FollowupCancelEnabled
	config.Config.FollowupCancelEnabled = true
	t.Cleanup(func() { config.Config.FollowupCancelEnabled = origFlag })

	ctx := security.NewRequestContextForSuperAdmin()

	cases := []struct {
		name         string
		request      ConversationApiRequest
		expectStatus int
		expectErrSub string
	}{
		{
			name:         "missing conversation_id is rejected",
			request:      ConversationApiRequest{Resolution: "dismiss"},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "conversation_id is required",
		},
		{
			name: "unsupported resolution is rejected",
			request: ConversationApiRequest{
				ConversationId: "00000000-0000-0000-0000-000000000000",
				Resolution:     "approve",
			},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "unsupported resolution",
		},
		{
			name: "missing agent_id is rejected",
			request: ConversationApiRequest{
				ConversationId: "00000000-0000-0000-0000-000000000000",
				Resolution:     "dismiss",
			},
			expectStatus: http.StatusBadRequest,
			expectErrSub: "agent_id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, status, err := handleFollowupCancel(ctx, tc.request)
			assert.Nil(t, resp)
			assert.Equal(t, tc.expectStatus, status)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.expectErrSub)
			}
		})
	}
}
