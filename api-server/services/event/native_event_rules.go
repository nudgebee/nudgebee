package event

import (
	"sync"

	"nudgebee/services/eventrule"
	"nudgebee/services/security"
)

// defaultNativeEventRuleSource is used when an emitted event carries no source.
const defaultNativeEventRuleSource = "nudgebee"

// nativeEventRuleSeen dedupes registration attempts within a process: at most one
// attempt per (tenant, account, aggregation_key) per lifetime, so the existence
// check + CreateEventRule stay off the per-event hot path once a type is known.
var nativeEventRuleSeen sync.Map

// registerNativeEventTypeRule makes an event's aggregation_key selectable as a
// workflow-trigger "Event Type" by ensuring an event_rules row exists for it.
// This keeps event_rules the single catalog of all event types — including
// Nudgebee-native ones (Anomaly, the Kubernetes-API-failure enrichers) that are
// emitted as events and never registered as alerts.
//
// It mirrors the webhook ingestion pattern (e.g. integrations/datadog_webhook.go):
// on a background goroutine, register the rule via eventrule.CreateEventRule —
// but only when one does not already exist, so CreateEventRule's ON CONFLICT
// DO UPDATE never overwrites a real prometheus/webhook/user rule that shares the
// alert name. The row carries the event's real source/category (so the Event
// Rules table stays filterable by source) and SkipPlaybook (no agent playbook to
// run, like webhook-ingested rules).
func registerNativeEventTypeRule(ctx *security.RequestContext, evt map[string]any) {
	accountID, _ := evt["cloud_account_id"].(string)
	tenantID, _ := evt["tenant"].(string)
	alert, _ := evt["aggregation_key"].(string)
	if accountID == "" || tenantID == "" || alert == "" {
		return
	}

	key := tenantID + ":" + accountID + ":" + alert
	if _, loaded := nativeEventRuleSeen.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	source, _ := evt["source"].(string)
	if source == "" {
		source = defaultNativeEventRuleSource
	}
	category, _ := evt["category"].(string)
	if category == "" {
		category = source
	}

	go func() {
		exists, err := eventrule.EventRuleExists(ctx, accountID, alert)
		if err != nil {
			nativeEventRuleSeen.Delete(key) // allow retry on a later event
			ctx.GetLogger().Error("native event rule: existence check failed", "alert", alert, "error", err)
			return
		}
		if exists {
			return // a real or previously-registered rule already covers this alert
		}

		if err := eventrule.EnsureEventRuleSource(ctx, source); err != nil {
			nativeEventRuleSeen.Delete(key) // allow retry on a later event
			ctx.GetLogger().Error("native event rule: source upsert failed", "source", source, "error", err)
			return
		}

		req := eventrule.EventConfig{
			Alert:        alert,
			AccountID:    accountID,
			Source:       source,
			Category:     category,
			Severity:     "warning",
			Enabled:      true,
			AlertType:    "log", // non-metric: never normalized to prometheus / synced to the relay
			SkipPlaybook: true,
		}
		req.Labels.Severity = "warning"
		if _, err := eventrule.CreateEventRule(ctx, req); err != nil {
			nativeEventRuleSeen.Delete(key) // allow retry on a later event
			ctx.GetLogger().Error("native event rule: CreateEventRule failed", "alert", alert, "account_id", accountID, "error", err)
		}
	}()
}
