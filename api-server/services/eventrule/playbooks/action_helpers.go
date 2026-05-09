package playbooks

func getPlatformDisplayName(platform string) string {
	switch platform {
	case "slack":
		return "Slack"
	case "teams":
		return "Microsoft Teams"
	case "gchat":
		return "Google Chat"
	default:
		return platform
	}
}
