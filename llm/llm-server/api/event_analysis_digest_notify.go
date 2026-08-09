package api

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// digestNotificationType is the notifications-server template key. It must match
// the entry added to that service's template_mapping — an unknown type falls
// through to the "default" template, which renders the raw parameters.
const digestNotificationType = "weekly_digest_events"

// digestNotificationSource is the notification-rule source users bind channels
// to. It reaches NotificationRuleMatcher's catch-all branch, which filters on
// (tenant, source) and ignores account / namespace / workload — correct here,
// because the review is one tenant-wide document rather than a per-account
// alert. Slack, Teams, Google Chat, Discord and email all route off this.
const digestNotificationSource = "weekly_digest"

// digestNotifyTopFindings is how many findings ride in the message body. The
// rest are counted as "+N more" behind the deep link. Slack caps a message at
// 50 blocks and 3000 characters per text block, and a busy tenant-week produces
// 30 findings — the full review does not fit and is not meant to. This mirrors
// recommendation_nudge_digest, which shows 3 of its ranked list for the same
// reason.
const digestNotifyTopFindings = 3

// digestNotifyBatch bounds one sweep. Larger than any realistic backlog (a
// tenant generates one digest a week and the scan window is 3 weeks), so it is
// a runaway guard rather than paging.
const digestNotifyBatch = 200

// notifyGeneratedDigests pushes completed tenant-weeks to their configured
// notification channels.
//
// Runs as a second pass after generation rather than inline with it, so a
// publish failure retries on the next tick instead of failing the digest that
// was already paid for, and so an llm-server restart between generating and
// publishing does not lose the delivery.
//
// Errors are logged per digest and never abort the sweep: one tenant's dead
// exchange must not stop another tenant's review from going out.
func notifyGeneratedDigests(ctx *security.RequestContext) {
	if config.Config.RabbitMqNotificationsExchange == "" || config.Config.RabbitMqNotificationsQueue == "" {
		ctx.GetLogger().Warn("digest: notifications exchange not configured, skipping delivery")
		return
	}

	pending, err := events.FindUndeliveredDigests(ctx, digestNotifyBatch)
	if err != nil {
		ctx.GetLogger().Error("digest: finding undelivered digests failed", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}

	var sent, skipped int
	for _, d := range pending {
		// Both stored JSON columns are checked here, before the claim is taken.
		// Neither is retryable — the row would be re-read identically on every
		// tick — so they are marked delivered rather than released. Metrics is
		// parsed again inside buildDigestNotification; doing it twice per
		// tenant-week is cheaper than letting a corrupt row release its claim
		// and spin on the publish path forever.
		findings, perr := parseClassFindings(d.ClassSummaries)
		if perr != nil {
			ctx.GetLogger().Error("digest: unreadable class summaries, not delivering",
				"error", perr, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
			markDelivered(ctx, d)
			continue
		}
		var metrics events.DigestMetrics
		if merr := json.Unmarshal(d.Metrics, &metrics); merr != nil {
			ctx.GetLogger().Error("digest: unreadable metrics, not delivering",
				"error", merr, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
			markDelivered(ctx, d)
			continue
		}
		if len(findings) == 0 {
			// A week where nothing broke. Marked delivered rather than left
			// pending so it stops being re-examined; there is no message worth
			// sending, which is the same call api-server's daily report makes
			// when its payload comes back empty.
			skipped++
			markDelivered(ctx, d)
			continue
		}

		// Claim before publishing: marking afterwards would re-send the whole
		// review if the process died in between. The claim is released again on
		// a failed publish (below), so this ordering does not turn an exchange
		// outage into a permanently skipped week.
		claimed, merr := events.MarkDigestDelivered(ctx, d.ID)
		if merr != nil {
			ctx.GetLogger().Error("digest: marking delivered failed, skipping publish",
				"error", merr, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
			continue
		}
		if !claimed {
			// Another publisher took this row between the scan and here.
			continue
		}

		if perr := publishDigestNotification(ctx, d, findings); perr != nil {
			ctx.GetLogger().Error("digest: publishing notification failed",
				"error", perr, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
			// Hand the claim back so the next tick retries. Without this a
			// single unreachable exchange would consume every tenant's pending
			// review and none would ever be sent.
			if rerr := events.ReleaseDigestDelivery(ctx, d.ID); rerr != nil {
				ctx.GetLogger().Error("digest: releasing delivery claim failed, week will not be retried",
					"error", rerr, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
			}
			continue
		}
		sent++
	}

	ctx.GetLogger().Info("digest: delivery complete",
		"sent", sent, "skipped_empty", skipped, "candidates", len(pending))
}

// markDelivered stamps a row we deliberately are not sending, so it leaves the
// pending scan. Failure is logged only — the row simply gets re-examined next
// tick and reaches the same conclusion.
func markDelivered(ctx *security.RequestContext, d events.Digest) {
	if _, err := events.MarkDigestDelivered(ctx, d.ID); err != nil {
		ctx.GetLogger().Error("digest: marking delivered failed",
			"error", err, "tenant_id", d.TenantID, "period_start", d.PeriodStart)
	}
}

// parseClassFindings decodes the stored class_summaries column. A NULL or
// JSON-null column is an empty week, not a parse error.
func parseClassFindings(raw json.RawMessage) ([]classFinding, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var findings []classFinding
	if err := json.Unmarshal(raw, &findings); err != nil {
		return nil, fmt.Errorf("parseClassFindings: %w", err)
	}
	return findings, nil
}

// rankFindingsForNotification orders findings by what a reader needs to see
// first: priority, then how many weeks the class has already been reported
// (a pattern nobody has fixed outranks a one-off), then volume.
//
// Deliberately not the briefing's own ordering — the briefing groups by cause
// family for reading, which is the wrong shape for "the three things to look at".
func rankFindingsForNotification(findings []classFinding) []classFinding {
	ranked := make([]classFinding, len(findings))
	copy(ranked, findings)

	priorityRank := func(p string) int {
		switch strings.ToUpper(strings.TrimSpace(p)) {
		case "P1", "HIGH", "CRITICAL":
			return 0
		case "P2", "MEDIUM":
			return 1
		default:
			return 2
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if pi, pj := priorityRank(ranked[i].Priority), priorityRank(ranked[j].Priority); pi != pj {
			return pi < pj
		}
		if ranked[i].CarriedOverWeeks != ranked[j].CarriedOverWeeks {
			return ranked[i].CarriedOverWeeks > ranked[j].CarriedOverWeeks
		}
		return ranked[i].Events > ranked[j].Events
	})
	return ranked
}

// publishDigestNotification emits one tenant-week to the notifications exchange.
//
// The payload is a summary, not the review: scoreboard counters, the top
// findings, and a link back to the tab. Two reasons it is not the whole thing —
// channel block limits, and the review names every account in the tenant, which
// reads very differently in a chat channel than behind the tab's tenant-scoped
// permission check.
func publishDigestNotification(ctx *security.RequestContext, d events.Digest, findings []classFinding) error {
	message, top, err := buildDigestNotification(d, findings)
	if err != nil {
		return err
	}

	if err := common.MqPublishWithContext(ctx.GetContext(),
		config.Config.RabbitMqNotificationsExchange,
		config.Config.RabbitMqNotificationsQueue,
		message); err != nil {
		return fmt.Errorf("publishDigestNotification: publish: %w", err)
	}

	ctx.GetLogger().Info("digest: notification published",
		"tenant_id", d.TenantID, "period_start", d.PeriodStart,
		"findings", len(findings), "in_message", top)
	return nil
}

// buildDigestNotification assembles the envelope notifications-server consumes.
//
// Split from the publish so the exact payload can be asserted and dumped
// without a broker — the envelope's field names are a cross-language contract
// with the Python templates, and a typo in one of them is invisible until a
// message renders empty.
func buildDigestNotification(d events.Digest, findings []classFinding) (map[string]any, int, error) {
	var metrics events.DigestMetrics
	if err := json.Unmarshal(d.Metrics, &metrics); err != nil {
		return nil, 0, fmt.Errorf("buildDigestNotification: metrics: %w", err)
	}

	ranked := rankFindingsForNotification(findings)
	top := ranked
	if len(top) > digestNotifyTopFindings {
		top = top[:digestNotifyTopFindings]
	}

	topPayload := make([]map[string]any, 0, len(top))
	for _, f := range top {
		topPayload = append(topPayload, map[string]any{
			"label":              f.Label,
			"aggregation_key":    f.AggregationKey,
			"account_name":       f.AccountName,
			"cloud_account_id":   f.AccountID,
			"headline":           f.Headline,
			"priority":           f.Priority,
			"cause":              f.Cause,
			"env":                f.Env,
			"events":             f.Events,
			"carried_over_weeks": f.CarriedOverWeeks,
		})
	}

	// period_end is stored as the exclusive end of the week; the label a reader
	// expects is the last day covered.
	lastDay := d.PeriodEnd.AddDate(0, 0, -1)

	message := map[string]any{
		"kind":      "notification",
		"type":      digestNotificationType,
		"source":    digestNotificationSource,
		"tenant_id": d.TenantID,
		"parameters": map[string]any{
			"title":        "Weekly Digest",
			"period_start": d.PeriodStart.Format("2006-01-02"),
			"period_end":   lastDay.Format("2006-01-02"),
			"period_label": d.PeriodStart.Format("Jan 2") + " – " + lastDay.Format("Jan 2"),

			// Scoreboard, matching the four figures the tab leads with.
			"events_analysed": metrics.EventsAnalysed,
			"events_complete": metrics.EventsComplete,
			"completion_pct":  metrics.CompletionPct,
			"failed_events":   metrics.FailedEvents,
			"failure_classes": metrics.FailureClasses,
			"services":        metrics.Services,
			"new_incidents":   metrics.NewIncidents,
			"recurrences":     metrics.Recurrences,
			"recurrence_pct":  metrics.RecurrencePct,
			"noise_pct":       metrics.NoisePct,
			"p1_pct":          metrics.P1Pct,

			"lede":            d.Summary,
			"top_findings":    topPayload,
			"total_findings":  len(findings),
			"more_findings":   maxInt(len(findings)-len(top), 0),
			"accounts_named":  countAccounts(findings),
			"base_url":        config.Config.BaseUrl,
			"digest_url":      digestDeepLink(config.Config.BaseUrl),
			"organization_id": d.TenantID,
		},
	}

	return message, len(top), nil
}

// digestDeepLink builds the "view the full review" target.
//
// b-Cortex is a modal with no route of its own, so the app reads this param on
// load and opens the Digests tab. The trailing slash is trimmed first: a
// configured base URL ending in "/" would otherwise produce "//?bcortex=..",
// which resolves to a protocol-relative host in some clients.
// The link lands on /home rather than /: the root route redirects to /home
// without carrying its query, which drops the param before the app can read it.
// An empty base URL yields "" rather than a bare "/home?bcortex=digests":
// a relative path is meaningless in a chat client, and because the renderer
// treats any non-empty digest_url as authoritative, emitting one would
// suppress its own base-URL fallback and ship a dead link.
func digestDeepLink(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return ""
	}
	return trimmed + "/home?bcortex=digests"
}

// countAccounts reports how many distinct accounts contributed a finding, so
// the message can say the review spans several without listing them all.
func countAccounts(findings []classFinding) int {
	seen := make(map[string]struct{}, len(findings))
	for _, f := range findings {
		if f.AccountID != "" {
			seen[f.AccountID] = struct{}{}
		}
	}
	return len(seen)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
