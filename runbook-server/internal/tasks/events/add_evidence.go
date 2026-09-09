package events

import (
	"errors"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/service"
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

// Execute runs the core logic of the task.
func (t *EventsAddEvidenceTask) Execute(taskCtx types.TaskContext, params map[string]any) (any, error) {
	eventId, _ := params["event_id"].(string)
	if eventId == "" {
		return nil, errors.New("event_id is required")
	}

	raw, ok := params["evidences"]
	if !ok || raw == nil {
		return nil, errors.New("evidences is required")
	}

	// The workflow builder hands this over as a list; a single object is
	// accepted too so a one-evidence step does not need list syntax.
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

	if err := service.AddEventEvidence(taskCtx.GetTenantID(), eventId, evidences); err != nil {
		taskCtx.GetLogger().Error("events.add_evidence: failed",
			"tenant", taskCtx.GetTenantID(), "event_id", eventId, "error", err)
		return nil, err
	}

	taskCtx.GetLogger().Info("events.add_evidence: evidence attached",
		"event_id", eventId, "count", len(evidences))
	return map[string]any{"event_id": eventId, "added": len(evidences)}, nil
}

// InputSchema returns the schema for the task's expected parameters.
func (t *EventsAddEvidenceTask) InputSchema() *types.Schema {
	return &types.Schema{
		Properties: map[string]types.Property{
			"event_id": {
				Type:        types.PropertyTypeString,
				Description: "Id of the event to attach evidence to. For an event-triggered workflow this is {{ Inputs.event.id }}.",
				Required:    true,
				Order:       1,
			},
			"evidences": {
				Type:        types.PropertyTypeArray,
				Description: "Evidence objects to append. Note that the Investigate page renders evidence by a fixed action-name list, so set additional_info.actual_action_name to text_enricher for a markdown card to be visible.",
				Required:    true,
				Order:       2,
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
