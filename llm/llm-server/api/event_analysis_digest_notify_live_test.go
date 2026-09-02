package api

import (
	"encoding/json"
	"os"
	"testing"

	"nudgebee/llm/config"
	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDigestDeliveryLiveSelection runs the real delivery scan against a real
// database. Gated on DIGEST_NOTIFY_LIVE_TEST because it reads production-shaped
// data; it does not publish or mutate anything by itself.
//
//	DIGEST_NOTIFY_LIVE_TEST=1 go test -v -run TestDigestDeliveryLiveSelection ./api/
//
// The point is the predicate, which unit tests cannot reach: a wrong filter
// here fails silently — it delivers nothing, or the wrong weeks — rather than
// erroring.
func TestDigestDeliveryLiveSelection(t *testing.T) {
	if os.Getenv("DIGEST_NOTIFY_LIVE_TEST") == "" {
		t.Skip("set DIGEST_NOTIFY_LIVE_TEST=1 to run against the live DB")
	}

	ctx, cancel := newDigestContext("")
	defer cancel()

	pending, err := events.FindUndeliveredDigests(ctx, digestNotifyBatch)
	require.NoError(t, err, "undelivered scan should succeed")

	t.Logf("selected %d undelivered digest(s)", len(pending))
	for _, d := range pending {
		findings, perr := parseClassFindings(d.ClassSummaries)
		require.NoError(t, perr, "stored class_summaries must parse")
		t.Logf("  week=%s tenant=%s status=%s source=%s findings=%d",
			d.PeriodStart.Format("2006-01-02"), d.TenantID, d.Status, d.Source, len(findings))

		// Every predicate the sweep depends on, asserted against real rows.
		assert.Equal(t, events.DigestStatusGenerated, d.Status, "partial/failed weeks must not be delivered")
		assert.Equal(t, events.DigestSourceScheduled, d.Source, "on-demand previews must not consume a delivery")
	}

	// Oldest first, so a tenant that missed two weeks gets them in order.
	for i := 1; i < len(pending); i++ {
		assert.False(t, pending[i].PeriodStart.Before(pending[i-1].PeriodStart),
			"results must be ordered oldest-first")
	}
}

// TestDigestNotificationDumpEnvelope prints the exact envelope that would be
// published for a real stored digest, so it can be fed to notifications-server
// directly. The field names are a cross-language contract with the Python
// templates; a typo on either side renders an empty message rather than failing.
//
//	DIGEST_NOTIFY_DUMP=1 go test -v -run TestDigestNotificationDumpEnvelope ./api/
//
// DIGEST_NOTIFY_DUMP_WEEK pins a period_start (YYYY-MM-DD); otherwise the most
// recent digest with findings is used.
func TestDigestNotificationDumpEnvelope(t *testing.T) {
	if os.Getenv("DIGEST_NOTIFY_DUMP") == "" {
		t.Skip("set DIGEST_NOTIFY_DUMP=1 to dump a real envelope")
	}

	ctx, cancel := newDigestContext("")
	defer cancel()

	tenant := os.Getenv("DIGEST_NOTIFY_DUMP_TENANT")
	require.NotEmpty(t, tenant, "set DIGEST_NOTIFY_DUMP_TENANT")

	digests, err := events.ListDigests(ctx, tenant, 20)
	require.NoError(t, err)
	require.NotEmpty(t, digests, "tenant has no digests")

	want := os.Getenv("DIGEST_NOTIFY_DUMP_WEEK")
	var chosen *events.Digest
	for i := range digests {
		d := digests[i]
		if want != "" && d.PeriodStart.Format("2006-01-02") != want {
			continue
		}
		// ListDigests omits class_summaries, so re-read the full row.
		full, gerr := events.GetDigest(ctx, tenant, d.PeriodStart)
		require.NoError(t, gerr)
		findings, perr := parseClassFindings(full.ClassSummaries)
		require.NoError(t, perr)
		if len(findings) > 0 {
			chosen = &full
			break
		}
	}
	require.NotNil(t, chosen, "no digest with findings found")

	findings, err := parseClassFindings(chosen.ClassSummaries)
	require.NoError(t, err)

	message, top, err := buildDigestNotification(*chosen, findings)
	require.NoError(t, err)

	out, err := json.MarshalIndent(message, "", "  ")
	require.NoError(t, err)
	t.Logf("week=%s findings=%d in_message=%d", chosen.PeriodStart.Format("2006-01-02"), len(findings), top)

	path := os.Getenv("DIGEST_NOTIFY_DUMP_PATH")
	require.NotEmpty(t, path, "set DIGEST_NOTIFY_DUMP_PATH to write the envelope")
	require.NoError(t, os.WriteFile(path, out, 0o600))
	t.Logf("envelope written to %s (%d bytes)", path, len(out))
}

// TestDigestDeliveryLivePublish performs the real publish for every undelivered
// digest: it builds the payload, sends it to the notifications exchange and
// stamps notified_at. A configured weekly_digest rule will produce actual
// channel messages.
//
//	DIGEST_NOTIFY_LIVE_PUBLISH=1 go test -v -run TestDigestDeliveryLivePublish ./api/
func TestDigestDeliveryLivePublish(t *testing.T) {
	if os.Getenv("DIGEST_NOTIFY_LIVE_PUBLISH") == "" {
		t.Skip("set DIGEST_NOTIFY_LIVE_PUBLISH=1 to publish to the real exchange")
	}

	require.NotEmpty(t, config.Config.RabbitMqNotificationsExchange, "notifications exchange must be configured")
	t.Logf("publishing to exchange=%s queue=%s",
		config.Config.RabbitMqNotificationsExchange, config.Config.RabbitMqNotificationsQueue)

	ctx, cancel := newDigestContext("")
	defer cancel()

	before, err := events.FindUndeliveredDigests(ctx, digestNotifyBatch)
	require.NoError(t, err)
	t.Logf("%d digest(s) pending delivery", len(before))

	notifyGeneratedDigests(ctx)

	after, err := events.FindUndeliveredDigests(ctx, digestNotifyBatch)
	require.NoError(t, err)
	assert.Empty(t, after, "every candidate should be claimed after a successful sweep")
}
