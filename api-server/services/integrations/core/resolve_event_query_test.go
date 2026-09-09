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
	// satisfy it. A second id placeholder would mean the ids had drifted apart.
	// Scoped to the WHERE clause: the SET clause legitimately carries its own
	// parameters (ends_at is $5).
	_, where, found := strings.Cut(q, " where ")
	assert.True(t, found, "query must have a WHERE clause")
	assert.NotContains(t, where, "$5", "the two id predicates must share a single parameter")
}

// A resolve must stamp ends_at in the same statement that sets status. Consumers
// that measure how long an alert ran — triage.computeAlertQuality most visibly —
// count only rows where ends_at IS NOT NULL AND ends_at > starts_at, so a CLOSED
// row left open-ended reads as "never resolved", drives the rule's
// resolution_rate to 0, and gets a healthy alert classified "broken".
func TestResolveEventQuery_StampsEndsAt(t *testing.T) {
	q := strings.ToLower(resolveEventQuery)

	assert.Contains(t, q, "ends_at = coalesce(ends_at, $5)",
		"must stamp ends_at, preserving any end time the ingest path already recorded")
	assert.Contains(t, q, "set status = $4, ends_at =",
		"status and ends_at must be set together, so a close can never land without an end time")
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
