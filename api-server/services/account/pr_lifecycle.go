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
