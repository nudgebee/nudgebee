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

// FindOpenPRResolutionByURL returns the resolution id and table for an open
// agent PR matching the given URL, or empty strings if none exists.
// Delegates to adapter.FindOpenPRResolutionByURL.
func FindOpenPRResolutionByURL(prURL string) (resolutionID, tableName string, err error) {
	return adapter.FindOpenPRResolutionByURL(prURL)
}

// ProcessOpenPRResolution dispatches a followup for a single PR resolution row.
// Used by the GitHub webhook handler to react to PR events immediately, without
// waiting for the next cron tick. Delegates to adapter.ProcessOpenPRResolution.
func ProcessOpenPRResolution(ctx *security.RequestContext, resolutionID, tableName string) error {
	return adapter.ProcessOpenPRResolution(ctx, resolutionID, tableName)
}

// MarkPRResolutionTerminal retires a resolution when its PR is closed or merged,
// so the cron and future webhooks stop dispatching followups for it. Delegates
// to adapter.MarkPRResolutionTerminal.
func MarkPRResolutionTerminal(ctx *security.RequestContext, resolutionID, tableName string, merged bool) error {
	return adapter.MarkPRResolutionTerminal(ctx, resolutionID, tableName, merged)
}
