package events

import (
	"errors"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"nudgebee/runbook/services/service"
)

// EventsStoreTask defines a task that interacts with an LLM to generate a summary.
type EventsStoreTask struct{}

// GetName returns the unique name of the task.
func (t *EventsStoreTask) GetName() string {
	return "events.store"
}

// GetDescription returns a brief description of the task.
func (t *EventsStoreTask) GetDescription() string {
	return "Save data to Nudgebee for later troubleshooting and analysis."
}

// GetDisplayName returns a human-readable name for the task.
func (t *EventsStoreTask) GetDisplayName() string {
	return "Store Event"
}

// Execute runs the core logic of the task.
func (t *EventsStoreTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	event := service.Event{}
	if _, ok := params["event"]; !ok {
		return nil, errors.New("event is required")
	}

	logger := taskCtx.GetLogger()

	if eventMap, ok := params["event"].(map[string]any); ok {
		if err := common.DecodeMapToStruct(eventMap, &event); err != nil {
			logger.Error("events.store: failed to decode event params", "tenant", taskCtx.GetTenantID(), "account_id", taskCtx.GetAccountID(), "error", err)
			return nil, err
		}
	}

	if event.AccountId == "" {
		event.AccountId = taskCtx.GetAccountID()
	}
	event.Tenant = taskCtx.GetTenantID()

	// Cluster is required by the events backend but is not surfaced in the
	// task UI (most non-K8s workflow events have no meaningful cluster).
	// Default it to the account name so the persisted event still carries
	// a recognizable scope.
	if event.Cluster == "" {
		accountName, err := cloud.GetAccountName(event.Tenant, event.AccountId)
		if err != nil {
			logger.Error("events.store: failed to get account name for cluster", "tenant", event.Tenant, "account_id", event.AccountId, "error", err)
			return nil, err
		}
		event.Cluster = accountName
	}

	if event.Evidences == nil {
		event.Evidences = []any{}
	}

	if event.Priority == "" {
		event.Priority = service.EventPriorityInfo
	}

	if event.Source == "" {
		event.Source = "automation"
	}

	if err := common.ValidateStruct(event); err != nil {
		logger.Error("events.store: validation failed", "tenant", event.Tenant, "finding_id", event.FindingId, "error", err)
		return nil, err
	}

	logger.Info("events.store: submitting event", "tenant", event.Tenant, "finding_id", event.FindingId, "aggregation_key", event.AggregationKey, "source", event.Source, "status", event.Status, "cluster", event.Cluster, "account_id", event.AccountId)

	ids, err := service.InvestigateEvent(taskCtx.GetTenantID(), []service.Event{event})
	if err != nil {
		logger.Error("events.store: investigation failed", "tenant", event.Tenant, "finding_id", event.FindingId, "error", err)
		return nil, err
	}
	if len(ids) == 0 {
		logger.Error("events.store: no event id returned", "tenant", event.Tenant, "finding_id", event.FindingId)
		return nil, errors.New("events: unable to process request")
	}

	logger.Info("events.store: event stored", "tenant", event.Tenant, "event_id", ids[0], "finding_id", event.FindingId)
	return ids[0], nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *EventsStoreTask) InputSchema() *types.Schema {
	evidenceSchema := &types.Schema{
		Properties: map[string]types.Property{
			"type": {
				Type:        types.PropertyTypeString,
				Description: "Evidence Type",
				Required:    false,
				Default:     "markdown",
			},
			"data": {
				Type:        types.PropertyTypeObject,
				Description: "Evidence Data",
				Required:    true,
			},
			"filename": {
				Type:        types.PropertyTypeString,
				Description: "Evidence FileName",
				Required:    true,
			},
			"additional_info": {
				Type:        types.PropertyTypeObject,
				Description: "Additional Details for Evidence",
				Required:    false,
			},
		},
	}

	eventSchema := &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
			},
			"title": {
				Type:        types.PropertyTypeString,
				Description: "Title for Event",
				Required:    true,
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "Description for Event",
				Required:    true,
			},
			"aggregation_key": {
				Type:        types.PropertyTypeString,
				Description: "Event Type",
				Required:    true,
			},
			"finding_id": {
				Type:        types.PropertyTypeString,
				Description: "Unique Event ID at Source. Events with the same finding_id, tenant, and account will update the existing event instead of creating a new one.",
				Required:    true,
			},
			"finding_type": {
				Type:        types.PropertyTypeString,
				Description: "Event Type At Source",
				Required:    true,
			},
			"subject_name": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Name",
				Required:    true,
			},
			"priority": {
				Type:        types.PropertyTypeString,
				Description: "Event Priority",
				Required:    true,
				Options:     []string{"DEBUG", "INFO", "LOW", "MEDIUM", "HIGH"},
				Default:     "INFO",
			},
			"source": {
				Type:        types.PropertyTypeString,
				Description: "Event Source",
				Required:    false,
				Options:     []string{"kubernetes_api_server", "prometheus", "manual", "helm_release", "callback", "scheduler", "nudgebee", "AWS_CloudTrail", "AWS_CloudWatch_Alarm", "pagerduty_webhook", "anomaly", "slo", "AWS_EventBridge", "datadog_webhook", "chronosphere", "servicenow_webhook", "azure_monitor_webhook", "automation"},
				Default:     "automation",
			},
			"category": {
				Type:        types.PropertyTypeString,
				Description: "Event Category",
				Required:    false,
			},
			"subject_type": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Type",
				Required:    false,
			},
			"subject_namespace": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Namespace",
				Required:    false,
			},
			"subject_node": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Node",
				Required:    false,
			},
			"service_key": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Service Key",
				Required:    false,
			},
			"ends_at": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Event Ended At",
				Required:    false,
			},
			"starts_at": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Event Started Time, in ISO format",
				Required:    false,
			},
			"fingerprint": {
				Type:        types.PropertyTypeString,
				Description: "Event Fingerprint for Deduplication",
				Required:    false,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "Event Status",
				Required:    true,
				Options:     []string{"FIRING", "RESOLVED", "CLOSED"},
			},
			"principal": {
				Type:        types.PropertyTypeString,
				Description: "Event User",
				Required:    false,
			},
			"subject_owner": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject parent",
				Required:    false,
			},
			"subject_owner_kind": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Parent Type",
				Required:    false,
			},
			"labels": {
				Type:        types.PropertyTypeObject,
				Description: "Labels to Use for auto investigation",
				Required:    false,
			},
			"evidences": {
				Type:        types.PropertyTypeArray,
				Description: "Evidences for Event",
				Required:    false,
				Schema:      evidenceSchema,
			},
		},
	}

	return &types.Schema{
		Properties: map[string]types.Property{
			"event": {
				Type:        types.PropertyTypeObject,
				Description: "Event To Store",
				Required:    true,
				Schema:      eventSchema,
			},
			"trigger_notification_im": {
				Type:        types.PropertyTypeBoolean,
				Description: "Auto Trigger IM notifications",
				Required:    false,
				Default:     true,
			},
			"trigger_ai_analysis": {
				Type:        types.PropertyTypeBoolean,
				Description: "Auto Trigger AI Analysis",
				Required:    false,
				Default:     true,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *EventsStoreTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"id": {
				Type:        types.PropertyTypeString,
				Description: "Event Id",
				Required:    true,
			},
		},
	}
}
