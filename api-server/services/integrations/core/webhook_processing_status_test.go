package core

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An event type the integration does not model is a no-op, not a failure.
// PagerDuty fans out incident.acknowledged / incident.annotated /
// incident.custom_field_values.updated to every subscription, so recording those
// as failures made ingestion look broken and hid the real failures.
func TestWebhookProcessingStatusForError(t *testing.T) {
	assert.Equal(t, webhookProcessingStatusSkipped, webhookProcessingStatusForError(ErrEventNotSupported))

	// Integrations return the sentinel wrapped as often as bare.
	wrapped := fmt.Errorf("pagerdutywebhook: %w", ErrEventNotSupported)
	assert.Equal(t, webhookProcessingStatusSkipped, webhookProcessingStatusForError(wrapped))

	// Anything else is a genuine failure and must stay visible as one.
	assert.Equal(t, webhookProcessingStatusFailed, webhookProcessingStatusForError(errors.New("invalid payload, event.data.id not found")))
}

// The three statuses must stay distinct: "skipped" exists precisely to be
// filterable apart from "failed", and "processed" is the success path.
func TestWebhookProcessingStatusesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []string{webhookProcessingStatusProcessed, webhookProcessingStatusFailed, webhookProcessingStatusSkipped} {
		assert.NotEmpty(t, s)
		assert.False(t, seen[s], "duplicate processing_status value %q", s)
		seen[s] = true
	}
}
