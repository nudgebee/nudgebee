package account

import (
	"nudgebee/services/account/adapter"
	"nudgebee/services/security"
)

// CheckAndFollowupOpenPRs polls resolution tables for open agent PRs and triggers followup.
// Delegates to adapter.CheckAndFollowupOpenPRs.
func CheckAndFollowupOpenPRs(ctx *security.RequestContext) error {
	return adapter.CheckAndFollowupOpenPRs(ctx)
}

// HasOpenPRResolutionForURL reports whether we own an open agent PR matching
// the given URL. Delegates to adapter.HasOpenPRResolutionForURL.
func HasOpenPRResolutionForURL(prURL string) (bool, error) {
	return adapter.HasOpenPRResolutionForURL(prURL)
}

// ProcessOpenPRFollowup dispatches a followup for a single PR, identified by
// its URL. Used by the GitHub webhook handler to react to PR events
// immediately, without waiting for the next cron tick. Delegates to
// adapter.ProcessOpenPRFollowup.
func ProcessOpenPRFollowup(ctx *security.RequestContext, prURL string) error {
	return adapter.ProcessOpenPRFollowup(ctx, prURL)
}

// MarkAllPRResolutionsTerminalByURL retires every open resolution (across both
// resolution tables) and the pr_followup row whose PR just closed or merged,
// flipping pr_lifecycle_state and the user-facing status. Delegates to
// adapter.MarkAllPRResolutionsTerminalByURL.
func MarkAllPRResolutionsTerminalByURL(ctx *security.RequestContext, prURL string, merged bool) (int64, error) {
	return adapter.MarkAllPRResolutionsTerminalByURL(ctx, prURL, merged)
}
