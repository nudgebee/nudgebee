package notification

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotDeliveredMessage(t *testing.T) {
	for _, tc := range []struct {
		platform, reason, want string
	}{
		{"ms_teams", ReasonNoDestination, "Microsoft Teams isn't connected for this tenant. Connect the Microsoft Teams app in Integrations, or pick another provider."},
		{"ms_teams", ReasonChannelNotFound, "No channel 'Cosmos Channel' found in Microsoft Teams. Check the channel name, or pick another channel."},
		{"slack", ReasonNotInChannel, "The Slack app isn't a member of 'Cosmos Channel'. Add it to the channel, or pick another channel."},
		{"slack", ReasonPermissionDenied, "The Slack app isn't allowed to post in 'Cosmos Channel'. Check its permissions in Slack, or pick another channel."},
		{"google_chat", ReasonAuthFailed, "Google Chat rejected the app's credentials. Reconnect Google Chat in Integrations."},
		{"google_chat", ReasonNotConfigured, "Google Chat is connected but not fully set up. Finish setting it up in Integrations."},
		{"slack", ReasonMessageRejected, "Slack rejected the message. Check its length and content."},
		{"ms_teams", ReasonInvalidRequest, "The step's settings are incomplete for Microsoft Teams. Check the channel and the team."},
		{"slack", ReasonInvalidRequest, "The step's settings are incomplete for Slack. Check the channel."},
		{"google_chat", ReasonInvalidRequest, "The step's settings are incomplete for Google Chat. Check the channel."},
		{"slack", ReasonRateLimited, "Slack isn't accepting messages right now. Try again shortly."},
		{"slack", ReasonPlatformUnavailable, "Slack isn't accepting messages right now. Try again shortly."},
		{"slack", ReasonInternalError, "Something went wrong sending the message to Slack. Try again, and contact support if it keeps failing."},
		{"slack", "some_new_reason", "The message wasn't sent to Slack (some_new_reason)."},
		{"discord", "", "The message wasn't sent to discord."},
	} {
		t.Run(tc.platform+"/"+tc.reason, func(t *testing.T) {
			assert.Equal(t, tc.want, NotDeliveredMessage(tc.platform, "Cosmos Channel", tc.reason))
		})
	}
}
