package events

import (
	"errors"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/cloud"
	"nudgebee/runbook/services/service"
	"strings"
)

// EventsStoreTask defines a task that interacts with an LLM to generate a summary.
type EventsStoreTask struct{}

// GetName returns the unique name of the task.
func (t *EventsStoreTask) GetName() string {
	return "events.store"
}

// GetDescription returns a brief description of the task.
func (t *EventsStoreTask) GetDescription() string {
	return "Create or update a Nudgebee event from the event fields and return its id. Evidences (markdown, JSON or table cards) are attached after the event is stored."
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

	if err := validateFindingId(event); err != nil {
		logger.Error("events.store: validation failed", "tenant", taskCtx.GetTenantID(), "status", event.Status, "error", err)
		return nil, err
	}

	// Checked before anything is stored, so a bad card fails the step with no side effect.
	legacyEvidences, authoredEvidences, err := splitEventEvidences(event.Evidences)
	if err != nil {
		logger.Error("events.store: evidence validation failed", "tenant", taskCtx.GetTenantID(), "error", err)
		return nil, err
	}
	event.Evidences = legacyEvidences

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

	if event.Source == "automation" {
		if event.Labels == nil {
			event.Labels = map[string]string{}
		}
		if _, ok := event.Labels["automation_id"]; !ok {
			event.Labels["automation_id"] = taskCtx.GetWorkflowID()
		}
		if _, ok := event.Labels["automation_execution_id"]; !ok {
			event.Labels["automation_execution_id"] = taskCtx.GetWorkflowRunID()
		}
		if _, ok := event.Labels["automation_name"]; !ok {
			event.Labels["automation_name"] = taskCtx.GetWorkflowName()
		}
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

	if len(authoredEvidences) > 0 {
		// A resolve by fingerprint that closed nothing returns no id to attach to.
		if ids[0] == "" {
			logger.Warn("events.store: no event to attach evidences to", "tenant", event.Tenant, "count", len(authoredEvidences))
		} else if err := attachAuthoredEvidences(taskCtx, ids[0], authoredEvidences); err != nil {
			return nil, err
		}
	}
	return ids[0], nil
}

// splitEventEvidences separates pre-built evidence objects, forwarded unchanged
// as before, from cards authored with the evidence type picker, which are built
// by api-server and attached once the event exists.
func splitEventEvidences(evidences []any) (legacy []any, authored []service.AuthoredEvidence, err error) {
	legacy = []any{}
	for i, item := range evidences {
		itemMap, ok := item.(map[string]any)
		if !ok || isLegacyEvidence(itemMap) {
			legacy = append(legacy, item)
			continue
		}
		evidence := buildAuthoredEvidence(itemMap)
		if err := validateAuthoredEvidence(evidence); err != nil {
			return nil, nil, fmt.Errorf("events.store: evidence %d: %w", i+1, err)
		}
		authored = append(authored, evidence)
	}
	return legacy, authored, nil
}

// isLegacyEvidence reports a pre-built evidence object. One carrying none of the
// authored fields also counts, so an older half-filled item is forwarded as before
// instead of failing a step that used to run.
func isLegacyEvidence(item map[string]any) bool {
	for _, key := range []string{"data", "filename", "additional_info", "insight"} {
		if _, ok := item[key]; ok {
			return true
		}
	}
	for _, key := range []string{"content", "json_data", "headers", "rows", "title", "summary", "severity"} {
		if _, ok := item[key]; ok {
			return false
		}
	}
	return true
}

// evidenceTaskID gives each card its own id: api-server replaces earlier output
// per (workflow, task), so a shared id would make each card delete the last.
func evidenceTaskID(taskID string, index int) string {
	return fmt.Sprintf("%s#evidence-%d", taskID, index+1)
}

func attachAuthoredEvidences(taskCtx types.TaskContext, eventId string, evidences []service.AuthoredEvidence) error {
	logger := taskCtx.GetLogger()
	var failures []string
	for i, evidence := range evidences {
		source := service.EvidenceSourceWorkflow{
			WorkflowID:   taskCtx.GetWorkflowID(),
			WorkflowName: taskCtx.GetWorkflowName(),
			ExecutionID:  taskCtx.GetWorkflowRunID(),
			TaskID:       evidenceTaskID(taskCtx.GetTaskID(), i),
		}
		if err := service.AddAuthoredEventEvidence(taskCtx.GetTenantID(), eventId, evidence, source); err != nil {
			logger.Error("events.store: failed to attach evidence", "event_id", eventId, "index", i+1, "type", evidence.Type, "error", err)
			failures = append(failures, fmt.Sprintf("evidence %d: %v", i+1, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("events.store: event %s was stored, but %d of %d evidences could not be attached: %s",
			eventId, len(failures), len(evidences), strings.Join(failures, "; "))
	}
	logger.Info("events.store: evidences attached", "event_id", eventId, "count", len(evidences))
	return nil
}

// validateFindingId requires finding_id except on a resolve that carries a
// fingerprint: that one closes every open event of the fingerprint instead of one
// named firing, for automations that do not track which firings are open.
func validateFindingId(event service.Event) error {
	if event.FindingId != "" {
		return nil
	}
	status := strings.ToUpper(string(event.Status))
	isResolve := status == string(service.EventStatusResolved) || status == string(service.EventStatusClosed)
	if isResolve && event.Fingerprint != "" {
		return nil
	}
	return errors.New("events.store: finding_id is required unless status is RESOLVED or CLOSED and fingerprint is set")
}

// InputSchema returns the schema for the task's expected parameters.
func (t *EventsStoreTask) InputSchema() *types.Schema {
	evidenceProperties := authoredEvidenceProperties()
	evidenceType := evidenceProperties["type"]
	// Array items are never run through Schema.Validate, so this only marks the
	// field in the form; Execute checks each card before storing anything.
	evidenceType.Required = true
	evidenceProperties["type"] = evidenceType
	evidenceSchema := &types.Schema{Properties: evidenceProperties}

	eventSchema := &types.Schema{
		Properties: map[string]types.Property{
			"account_id": {
				Type:        types.PropertyTypeAccount,
				Description: "NB Account Id",
				Required:    false,
				Order:       18,
			},
			"title": {
				Type:        types.PropertyTypeString,
				Description: "Title for Event",
				Required:    true,
				Order:       1,
			},
			"description": {
				Type:        types.PropertyTypeString,
				Description: "Description for Event",
				Required:    true,
				Order:       2,
			},
			"aggregation_key": {
				Type:        types.PropertyTypeString,
				Description: "Event Type",
				Required:    true,
				Order:       7,
			},
			"finding_id": {
				Type:         types.PropertyTypeString,
				Description:  "ID of one firing of the alert. A later event with the same finding_id updates that event instead of creating one, and status RESOLVED closes it. A firing after a close needs a new finding_id. Optional on RESOLVED/CLOSED when fingerprint is set.",
				Required:     false,
				RequiredWhen: &types.RequiredWhen{Field: "status", Value: []string{"FIRING"}},
				Order:        4,
			},
			"finding_type": {
				Type:        types.PropertyTypeString,
				Description: "Event Type At Source",
				Required:    true,
				Order:       8,
			},
			"subject_name": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Name",
				Required:    true,
				Order:       9,
			},
			"priority": {
				Type:        types.PropertyTypeString,
				Description: "Event Priority",
				Required:    true,
				Options:     []string{"DEBUG", "INFO", "LOW", "MEDIUM", "HIGH"},
				Default:     "INFO",
				Order:       6,
			},
			"source": {
				Type:        types.PropertyTypeString,
				Description: "Event Source",
				Required:    false,
				Options:     []string{"kubernetes_api_server", "prometheus", "manual", "helm_release", "callback", "scheduler", "nudgebee", "AWS_CloudTrail", "AWS_CloudWatch_Alarm", "pagerduty_webhook", "anomaly", "slo", "AWS_EventBridge", "datadog_webhook", "chronosphere", "servicenow_webhook", "azure_monitor_webhook", "automation"},
				Default:     "automation",
				Order:       16,
			},
			"category": {
				Type:        types.PropertyTypeString,
				Description: "Event Category",
				Required:    false,
				Order:       17,
			},
			"subject_type": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Type",
				Required:    false,
				Order:       10,
			},
			"subject_namespace": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Namespace",
				Required:    false,
				Order:       11,
			},
			"subject_node": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Node",
				Required:    false,
				Order:       12,
			},
			"service_key": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Service Key",
				Required:    false,
				Order:       15,
			},
			"ends_at": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Event Ended At",
				Required:    false,
				Order:       21,
			},
			"starts_at": {
				Type:        types.PropertyTypeTimestamp,
				Description: "Event Started Time, in ISO format",
				Required:    false,
				Order:       20,
			},
			"fingerprint": {
				Type:        types.PropertyTypeString,
				Description: "Stable identity of the alert, the same on every firing. RESOLVED with a fingerprint and no finding_id closes every open event of this fingerprint from the same source.",
				Required:    false,
				Order:       5,
			},
			"status": {
				Type:        types.PropertyTypeString,
				Description: "FIRING opens an event. RESOLVED closes the event named by finding_id, or every open event of the fingerprint when finding_id is omitted. It never reopens a closed event.",
				Required:    true,
				Options:     []string{"FIRING", "RESOLVED", "CLOSED"},
				Order:       3,
			},
			"principal": {
				Type:        types.PropertyTypeString,
				Description: "Event User",
				Required:    false,
				Order:       19,
			},
			"subject_owner": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject parent",
				Required:    false,
				Order:       13,
			},
			"subject_owner_kind": {
				Type:        types.PropertyTypeString,
				Description: "Event Subject Parent Type",
				Required:    false,
				Order:       14,
			},
			"labels": {
				Type:        types.PropertyTypeObject,
				Description: "Labels to Use for auto investigation",
				Required:    false,
				Order:       22,
			},
			"evidences": {
				Type:        types.PropertyTypeArray,
				Title:       "Evidences",
				Description: "Cards shown on the event page: a markdown note, a JSON document or a table. Attached after the event is stored.",
				Required:    false,
				Schema:      evidenceSchema,
				Order:       23,
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
				Order:       30,
			},
			"trigger_ai_analysis": {
				Type:        types.PropertyTypeBoolean,
				Description: "Auto Trigger AI Analysis",
				Required:    false,
				Default:     true,
				Order:       31,
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
