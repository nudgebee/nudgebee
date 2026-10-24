package notification

import "fmt"

// Reasons notifications-server gives on a failed or not_sent result.
const (
	ReasonNoDestination       = "no_destination"
	ReasonChannelNotFound     = "channel_not_found"
	ReasonNotInChannel        = "not_in_channel"
	ReasonPermissionDenied    = "permission_denied"
	ReasonAuthFailed          = "auth_failed"
	ReasonNotConfigured       = "not_configured"
	ReasonMessageRejected     = "message_rejected"
	ReasonInvalidRequest      = "invalid_request"
	ReasonRateLimited         = "rate_limited"
	ReasonPlatformUnavailable = "platform_unavailable"
	ReasonInternalError       = "internal_error"
	ReasonWebhookGone         = "webhook_gone"
)

var platformNames = map[string]string{
	"slack":               "Slack",
	"ms_teams":            "Microsoft Teams",
	"google_chat":         "Google Chat",
	"slack_webhook":       "Slack webhook",
	"ms_teams_webhook":    "Teams webhook",
	"google_chat_webhook": "Google Chat webhook",
}

// webhookApps names the chat app a webhook posts into.
var webhookApps = map[string]string{
	"slack_webhook":       "Slack",
	"ms_teams_webhook":    "Microsoft Teams",
	"google_chat_webhook": "Google Chat",
}

// PlatformName is how a message names platform to a user.
func PlatformName(platform string) string {
	if name, ok := platformNames[platform]; ok {
		return name
	}
	return platform
}

// NotDeliveredMessage says in plain words why a message to channel on
// platform was not posted, and what fixes it where something can.
func NotDeliveredMessage(platform, channel, reason string) string {
	name := PlatformName(platform)
	if app, ok := webhookApps[platform]; ok {
		if message, ok := webhookNotDeliveredMessage(name, app, reason); ok {
			return message
		}
	}
	switch reason {
	case ReasonNoDestination:
		return fmt.Sprintf("%s isn't connected for this tenant. Connect the %s app in Integrations, or pick another provider.", name, name)
	case ReasonChannelNotFound:
		return fmt.Sprintf("No channel '%s' found in %s. Check the channel name, or pick another channel.", channel, name)
	case ReasonNotInChannel:
		return fmt.Sprintf("The %s app isn't a member of '%s'. Add it to the channel, or pick another channel.", name, channel)
	case ReasonPermissionDenied:
		return fmt.Sprintf("The %s app isn't allowed to post in '%s'. Check its permissions in %s, or pick another channel.", name, channel, name)
	case ReasonAuthFailed:
		return fmt.Sprintf("%s rejected the app's credentials. Reconnect %s in Integrations.", name, name)
	case ReasonNotConfigured:
		return fmt.Sprintf("%s is connected but not fully set up. Finish setting it up in Integrations.", name)
	case ReasonMessageRejected:
		return fmt.Sprintf("%s rejected the message. Check its length and content.", name)
	case ReasonInvalidRequest:
		if platform == "ms_teams" {
			return fmt.Sprintf("The step's settings are incomplete for %s. Check the channel and the team.", name)
		}
		return fmt.Sprintf("The step's settings are incomplete for %s. Check the channel.", name)
	case ReasonRateLimited, ReasonPlatformUnavailable:
		return fmt.Sprintf("%s isn't accepting messages right now. Try again shortly.", name)
	case ReasonInternalError:
		return fmt.Sprintf("Something went wrong sending the message to %s. Try again, and contact support if it keeps failing.", name)
	case "":
		return fmt.Sprintf("The message wasn't sent to %s.", name)
	default:
		return fmt.Sprintf("The message wasn't sent to %s (%s).", name, reason)
	}
}

// webhookNotDeliveredMessage covers the reasons whose fix differs for a webhook:
// the channel is the integration, and there is no app to connect or invite.
func webhookNotDeliveredMessage(name, app, reason string) (string, bool) {
	switch reason {
	case ReasonNoDestination:
		return fmt.Sprintf("The %s this step posts to was removed or disabled. Pick another webhook in the step, or add it again in Integrations.", name), true
	case ReasonWebhookGone, ReasonChannelNotFound:
		return fmt.Sprintf("%s no longer accepts this webhook; it was removed or turned off there. Create a new one in %s and update the %s URL in Integrations.", app, app, name), true
	case ReasonAuthFailed, ReasonPermissionDenied:
		return fmt.Sprintf("%s refused the %s URL. Update the URL in Integrations.", app, name), true
	case ReasonNotConfigured:
		return fmt.Sprintf("The %s has no URL set. Add one in Integrations.", name), true
	case ReasonMessageRejected, ReasonInvalidRequest:
		return fmt.Sprintf("%s rejected the message. Check its length and content.", app), true
	case ReasonRateLimited, ReasonPlatformUnavailable:
		return fmt.Sprintf("%s isn't accepting messages right now. Try again shortly.", app), true
	case ReasonInternalError:
		return fmt.Sprintf("Something went wrong sending the message to the %s. Try again, and contact support if it keeps failing.", name), true
	}
	return "", false
}
