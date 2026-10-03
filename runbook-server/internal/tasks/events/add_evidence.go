package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
)

// The evidence types an author can pick, mirroring what api-server will build
// and the event page can draw.
const (
	EvidenceTypeMarkdown = "markdown"
	EvidenceTypeJson     = "json"
	EvidenceTypeTable    = "table"
)

// EventsAddEvidenceTask appends evidences to an event that already exists.
//
// This is the counterpart to events.store, not a replacement for it:
//   - events.store CREATES an event (and, for an existing finding, deliberately
//     leaves its evidences alone — see InvestigateEvent's duplicate branch).
//   - events.add_evidence attaches output to an event that is already there,
//     which is what an automation triggered BY an event needs in order to put
//     its findings on that same event rather than splitting one incident
//     across two rows.
type EventsAddEvidenceTask struct{}

// GetName returns the unique name of the task.
func (t *EventsAddEvidenceTask) GetName() string {
	return "events.add_evidence"
}

// GetDescription returns a brief description of the task.
func (t *EventsAddEvidenceTask) GetDescription() string {
	return "Attach evidence to an existing Nudgebee event."
}

// GetDisplayName returns a human-readable name for the task.
func (t *EventsAddEvidenceTask) GetDisplayName() string {
	return "Add Event Evidence"
}

// buildAuthoredEvidence reads the per-type fields an author filled in.
//
// json_data and rows are declared as strings because that is what the form
// produces, but an automation written as YAML — or by Nubi — can just as
// naturally pass a real array or object, so those are re-encoded rather than
// rejected.
func buildAuthoredEvidence(params map[string]any) service.AuthoredEvidence {
	return service.AuthoredEvidence{
		Type:     stringParam(params["type"]),
		Title:    stringParam(params["title"]),
		Summary:  stringParam(params["summary"]),
		Severity: stringParam(params["severity"]),
		Content:  stringParam(params["content"]),
		JsonData: jsonParam(params["json_data"]),
		Headers:  stringsParam(params["headers"]),
		Rows:     jsonParam(params["rows"]),
	}
}

func stringParam(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func jsonParam(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stringsParam(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok {
				values = append(values, text)
			} else {
				values = append(values, fmt.Sprintf("%v", item))
			}
		}
		return values
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return []string{trimmed}
		}
	}
	return nil
}

// Execute runs the core logic of the task.
func (t *EventsAddEvidenceTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	eventId, _ := params["event_id"].(string)
	if eventId == "" {
		return nil, errors.New("event_id is required")
	}

	// Identity of the run doing the attaching, so the Investigate page can name
	// the automation behind each card it produced.
	source := service.EvidenceSourceWorkflow{
		WorkflowID:   taskCtx.GetWorkflowID(),
		WorkflowName: taskCtx.GetWorkflowName(),
		ExecutionID:  taskCtx.GetWorkflowRunID(),
		TaskID:       taskCtx.GetTaskID(),
	}

	raw, hasRaw := params["evidences"]
	if raw == nil {
		hasRaw = false
	}
	evidenceType := stringParam(params["type"])

	// Refused rather than silently preferring one: picking a type in the builder
	// clears a legacy `evidences` (the field declares depends_on), so both being
	// set means an automation written by hand, where quietly ignoring the fields
	// the author filled in would be the worse answer.
	if evidenceType != "" && hasRaw {
		return nil, errors.New("set either type or evidences, not both: remove evidences to use the evidence type fields")
	}

	if !hasRaw {
		if evidenceType == "" {
			return nil, errors.New("type is required")
		}
		evidence := buildAuthoredEvidence(params)
		if err := service.AddAuthoredEventEvidence(taskCtx.GetTenantID(), eventId, evidence, source); err != nil {
			taskCtx.GetLogger().Error("events.add_evidence: failed",
				"tenant", taskCtx.GetTenantID(), "event_id", eventId, "type", evidenceType, "error", err)
			return nil, err
		}
		taskCtx.GetLogger().Info("events.add_evidence: evidence attached",
			"event_id", eventId, "type", evidenceType, "count", 1)
		return map[string]any{"event_id": eventId, "added": 1}, nil
	}

	// Pre-built evidence objects, from automations written before the form
	// existed. Forwarded unchanged.
	var evidences []any
	switch v := raw.(type) {
	case []any:
		evidences = v
	case map[string]any:
		evidences = []any{v}
	default:
		return nil, errors.New("evidences must be an object or an array of objects")
	}
	if len(evidences) == 0 {
		return nil, errors.New("evidences must not be empty")
	}

	if err := service.AddEventEvidence(taskCtx.GetTenantID(), eventId, evidences, source); err != nil {
		taskCtx.GetLogger().Error("events.add_evidence: failed",
			"tenant", taskCtx.GetTenantID(), "event_id", eventId, "error", err)
		return nil, err
	}

	taskCtx.GetLogger().Info("events.add_evidence: evidence attached",
		"event_id", eventId, "count", len(evidences))
	return map[string]any{"event_id": eventId, "added": len(evidences)}, nil
}

// InputSchema returns the schema for the task's expected parameters.
//
// Only the evidence types the event page can actually draw are offered. The
// fields each type takes appear once that type is picked, and api-server
// rejects a type it cannot display — replacing a single free-text box whose
// undocumented shape decided, silently, whether a card ever appeared.
func (t *EventsAddEvidenceTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"event_id": {
				Type:        types.PropertyTypeString,
				Description: "Id of the event to attach evidence to. For an event-triggered workflow this is {{ Inputs.event.id }}.",
				Required:    true,
				Order:       1,
			},
			"type": {
				Type:        types.PropertyTypeString,
				Title:       "Evidence Type",
				Description: "How the evidence is displayed on the event.",
				// Neither required nor defaulted, and both for the same reason:
				// Schema.Validate runs on every save, the builder stamps defaults
				// onto a step as soon as it is opened, and an automation written
				// before this form existed carries `evidences` instead. Either
				// would turn opening such a step into a save it cannot pass.
				Required: false,
				Options:  []string{EvidenceTypeMarkdown, EvidenceTypeJson, EvidenceTypeTable},
				Order:    2,
			},
			"title": {
				Type:        types.PropertyTypeString,
				Description: "Heading shown on the card.",
				Order:       3,
			},
			"summary": {
				Type:        types.PropertyTypeString,
				Description: "One-line highlight shown above the card, and included in the event's insights. Leave blank for none.",
				Order:       4,
			},
			"severity": {
				Type:        types.PropertyTypeString,
				Description: "Severity of the highlight. Only applies when a summary is set.",
				Options:     []string{"Info", "High", "Critical"},
				Order:       5,
				VisibleWhen: &types.VisibleWhen{Field: "type", Value: []string{EvidenceTypeMarkdown, EvidenceTypeJson, EvidenceTypeTable}},
			},
			"content": {
				Type:         types.PropertyTypeString,
				SubType:      "textarea",
				Description:  "Markdown body of the card. Values from earlier steps can be used, e.g. {{ Tasks.my_step.output.summary }}.",
				Order:        9,
				VisibleWhen:  &types.VisibleWhen{Field: "type", Value: []string{EvidenceTypeMarkdown}},
				RequiredWhen: &types.RequiredWhen{Field: "type", Value: []string{EvidenceTypeMarkdown}},
			},
			"json_data": {
				Type:         types.PropertyTypeString,
				Title:        "JSON",
				SubType:      "json",
				Description:  "A JSON object or array, shown as a key/value table.",
				Order:        6,
				VisibleWhen:  &types.VisibleWhen{Field: "type", Value: []string{EvidenceTypeJson}},
				RequiredWhen: &types.RequiredWhen{Field: "type", Value: []string{EvidenceTypeJson}},
				Examples: []types.PropertyExample{
					{Label: `{"replicas": 3, "image": "api:1.4.2"}`, Value: `{"replicas": 3, "image": "api:1.4.2"}`, Note: "Each key becomes a row."},
				},
			},
			"headers": {
				Type:         types.PropertyTypeArray,
				Description:  "Column names, in order.",
				Order:        7,
				VisibleWhen:  &types.VisibleWhen{Field: "type", Value: []string{EvidenceTypeTable}},
				RequiredWhen: &types.RequiredWhen{Field: "type", Value: []string{EvidenceTypeTable}},
			},
			"rows": {
				Type:         types.PropertyTypeString,
				SubType:      "json",
				Description:  "Rows as a JSON array of arrays — one inner array per row, cells in the same order as the columns.",
				Order:        8,
				VisibleWhen:  &types.VisibleWhen{Field: "type", Value: []string{EvidenceTypeTable}},
				RequiredWhen: &types.RequiredWhen{Field: "type", Value: []string{EvidenceTypeTable}},
				Examples: []types.PropertyExample{
					{Label: `[["api-7f9", "CrashLoopBackOff"]]`, Value: `[["api-7f9", "CrashLoopBackOff"]]`, Note: "One inner array per row."},
				},
			},
			// Superseded by the fields above, which let api-server build the card.
			// Hidden rather than removed: automations authored before them still
			// send it, and their evidence objects are forwarded unchanged.
			//
			// depends_on is what lets an author move such a step onto the new
			// fields: the builder drops a dependent field when its controlling
			// field changes, so picking a type clears the array that would
			// otherwise keep winning, invisibly, over everything they fill in.
			"evidences": {
				Type:        types.PropertyTypeArray,
				Description: "Deprecated. Pre-built evidence objects, forwarded as-is.",
				Required:    false,
				Hidden:      true,
				DependsOn:   []string{"type"},
				Order:       10,
			},
		},
	}
}

// OutputSchema returns the schema for the task's output.
func (t *EventsAddEvidenceTask) OutputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"event_id": {
				Type:        types.PropertyTypeString,
				Description: "Id of the event that was updated.",
				Required:    true,
			},
			"added": {
				Type:        types.PropertyTypeNumber,
				Description: "Number of evidences appended.",
				Required:    true,
			},
		},
	}
}
