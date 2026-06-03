package queue

// EventPostProcessMessage represents a message for async event post-processing
type EventPostProcessMessage struct {
	EventID string `json:"event_id"`
}
