package core

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveEventQuery_MatchesFindingIdAndFingerprint pins the fix for PagerDuty
// resolves never closing anything. resolveEvent looks the event up by the id the
// resolve delivery carries (the PagerDuty incident id), but the PagerDuty parser
// overwrites Investigation.Fingerprint with the CEF dedup_key — stable across every
// incident for the same alert — so events.fingerprint never equals that id and the
// UPDATE matched zero rows. finding_id does equal it, because
// convertWebhookEventToEvent writes FindingId from EventId.
func TestResolveEventQuery_MatchesFindingIdAndFingerprint(t *testing.T) {
	q := strings.ToLower(resolveEventQuery)

	assert.Contains(t, q, "fingerprint = $3", "fingerprint must still match — sources that key on it depend on this")
	assert.Contains(t, q, "finding_id = $3", "finding_id must match too, or PagerDuty resolves close nothing")
	assert.Contains(t, q, "or", "the two id predicates must be an OR, not an AND")

	// Both predicates share $3, so the caller passes one id and either column may
	// satisfy it. A second placeholder would mean the ids had drifted apart.
	assert.NotContains(t, q, "$5", "the two id predicates must share a single parameter")
}

// The lookup must stay scoped to one tenant and one cloud account: fingerprints
// and finding ids are only unique within an account, and tenant is the leading
// column of events_cloudaccount_findingid, so dropping it costs the finding_id
// predicate its index.
func TestResolveEventQuery_IsTenantAndAccountScoped(t *testing.T) {
	q := strings.ToLower(resolveEventQuery)

	assert.Contains(t, q, "tenant = $1")
	assert.Contains(t, q, "cloud_account_id = $2")
}

// Closing an already-closed event must stay a no-op: resolveEvent publishes a
// resolved notification for every id the UPDATE returns, so without the guard a
// repeated resolve delivery would re-notify.
func TestResolveEventQuery_OnlyTransitionsOpenEvents(t *testing.T) {
	q := strings.ToLower(resolveEventQuery)

	assert.Contains(t, q, "status != $4")
	assert.Contains(t, q, "returning id")
}
