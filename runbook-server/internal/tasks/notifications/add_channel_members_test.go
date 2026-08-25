package notifications

import (
	"testing"

	"nudgebee/runbook/services/notification"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoMembersAddedError(t *testing.T) {
	testCases := []struct {
		name      string
		resp      notification.AddChannelMembersResponse
		expectErr bool
		contains  []string
	}{
		{
			name: "all users added",
			resp: notification.AddChannelMembersResponse{Added: []string{"U1", "U2"}},
		},
		{
			name: "all users already members",
			resp: notification.AddChannelMembersResponse{AlreadyMembers: []string{"U1"}},
		},
		{
			name: "partial failure still succeeds",
			resp: notification.AddChannelMembersResponse{
				Added:  []string{"U1"},
				Failed: []notification.AddMemberFailure{{UserID: "U2", Error: "user_is_restricted"}},
			},
		},
		{
			name: "nothing to do at all",
			resp: notification.AddChannelMembersResponse{},
		},
		{
			name: "every user failed",
			resp: notification.AddChannelMembersResponse{
				ChannelID: "C1",
				Failed: []notification.AddMemberFailure{
					{UserID: "U1", Error: "user_is_restricted"},
					{UserID: "U2", Error: "cant_invite"},
				},
			},
			expectErr: true,
			contains:  []string{"C1", "U1: user_is_restricted", "U2: cant_invite"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := noMembersAddedError(tc.resp)

			if !tc.expectErr {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestAddChannelMembersTask_Metadata(t *testing.T) {
	task := &AddChannelMembersTask{}

	assert.Equal(t, "notifications.add_channel_members", task.GetName())
	assert.NotEmpty(t, task.GetDescription())
	assert.NotEmpty(t, task.GetDisplayName())
}
