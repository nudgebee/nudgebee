package queue

// EventPostProcessMessage represents a message for async event post-processing
type EventPostProcessMessage struct {
	EventID string `json:"event_id"`
	// WorkflowOnly re-delivers an already-post-processed event to the runbook
	// workflow exchange WITHOUT re-running triage/llm/notification. Set when an
	// existing event re-fires (ON CONFLICT update while FIRING): the full pipeline
	// ran on first insert, but event-trigger workflows must still match every
	// occurrence — including workflows created after the event first appeared.
	WorkflowOnly bool `json:"workflow_only,omitempty"`
}
