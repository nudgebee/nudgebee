package account

import (
	"encoding/json"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
)

// WebhookEvent is the event row polling would store for an alert a cloud provider
// pushed by webhook (GCP Monitoring, Azure Monitor). api-server ingests webhook
// deliveries itself but takes their identity from here, so a pushed alert and a
// polled one agree on every field enrichment, dedup and resolve key on.
type WebhookEvent struct {
	Title            string            `json:"title"`
	Description      string            `json:"description"`
	AggregationKey   string            `json:"aggregation_key"`
	SubjectName      string            `json:"subject_name"`
	SubjectType      string            `json:"subject_type"`
	SubjectNamespace string            `json:"subject_namespace"`
	ServiceKey       string            `json:"service_key"`
	Fingerprint      string            `json:"fingerprint"`
	FindingId        string            `json:"finding_id"`
	CloudResourceId  string            `json:"cloud_resource_id"`
	Priority         string            `json:"priority"`
	Status           string            `json:"status"`
	Labels           map[string]string `json:"labels"`
}

// webhookEventRow runs the polling path's own row mapping rather than restating it,
// so the two cannot drift apart again.
func webhookEventRow(ctx *security.RequestContext, event providers.Event, account providers.Account, accountId string) (map[string]any, error) {
	row, err := prepareEventForDB(ctx, event, account, accountId)
	if err != nil {
		return nil, err
	}
	// Set by insertNewEvents, not prepareEventForDB.
	row["finding_id"] = event.FiringFindingID()
	return row, nil
}

func webhookEventFromRow(row map[string]any) (WebhookEvent, error) {
	// Round-trip through JSON: the row holds pointers (description, cloud_resource_id)
	// and typed enums.
	raw, err := json.Marshal(row)
	if err != nil {
		return WebhookEvent{}, err
	}
	var out WebhookEvent
	if err := json.Unmarshal(raw, &out); err != nil {
		return WebhookEvent{}, err
	}
	return out, nil
}
