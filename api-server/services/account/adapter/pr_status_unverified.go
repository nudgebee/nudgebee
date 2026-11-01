package adapter

import "time"

// PRStatusUnverifiedLimit is how long a pull request may go without a
// successful status check before its resolution is settled Failed anyway.
//
// A check that could not reach the provider — an expired or unconfigured
// credential, a 5xx, a rate limit — says nothing about the pull request, which
// may well still be open. Settling it Failed makes the recommendation look free
// and the next scheduled run raises a second pull request next to the first.
// Waiting forever is not right either: a revoked integration never answers
// again, and the recommendation would stay in progress for good. Seven days
// covers a long outage of our own configuration; after that, the recommendation
// is handed back.
const PRStatusUnverifiedLimit = 7 * 24 * time.Hour

// unverifiedPRStatus is the status check's answer when the provider could not
// be asked or did not answer: still in progress, with the reason shown.
func unverifiedPRStatus(reason string) GetRecommendationResolutionStatusResponse {
	return GetRecommendationResolutionStatusResponse{
		Status:        RecommendationResolutionStatusInProgress,
		StatusMessage: truncateForStatus("Could not check the pull request: " + reason),
		Unverified:    true,
	}
}

// PRStatusCheckExpired reports whether a resolution whose status check could
// not reach the provider has now gone unchecked for longer than
// PRStatusUnverifiedLimit. lastChecked is its last_pr_check_at, naive UTC; nil
// means no check has succeeded since the clock was introduced, and the caller
// starts it now.
func PRStatusCheckExpired(lastChecked *time.Time, now time.Time) bool {
	return lastChecked != nil && now.Sub(*lastChecked) > PRStatusUnverifiedLimit
}

// PRStatusGaveUpMessage is the status message for a resolution settled Failed
// because its pull request could not be checked for PRStatusUnverifiedLimit.
func PRStatusGaveUpMessage(lastError string) string {
	return truncateForStatus("Could not check the pull request for 7 days, so the recommendation is handed back. " + lastError)
}
