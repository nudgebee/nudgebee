package core

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A label-mapping template must not be able to read files from the server:
// whatever a template loads ends up in the event's subject name and namespace.
func TestApplyLabelMapping_TemplateCannotLoadFiles(t *testing.T) {
	const marker = "label-mapping-loader-marker-91d2"
	path := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(path, []byte(marker), 0o600))

	mapping := &WebhookLabelMapping{
		SubjectNameLabels: []string{`{{ labels.app }}{% include "` + path + `" %}`, "app"},
		NamespaceLabels:   []string{`{{ '' }}{% import "` + path + `" as x %}`},
	}
	event := EventIncomingWebhook{
		Investigation: EventIncomingWebhookInvestigation{Labels: map[string]string{"app": "checkout"}},
	}
	applyLabelMapping(&event, mapping, newLabelExtractorCache(slog.Default(), mapping), true)

	assert.NotContains(t, event.EventSubjectName, marker)
	assert.NotContains(t, event.EventSubjectNamespace, marker)
	// The refused template yields nothing, so the next spec still resolves.
	assert.Equal(t, "checkout", event.EventSubjectName)
}

// Ordinary templates, including the custom split filter, keep working.
func TestApplyLabelMapping_TemplateStillRenders(t *testing.T) {
	mapping := &WebhookLabelMapping{SubjectNameLabels: []string{`{{ labels.app_id | upper }}`}}
	event := EventIncomingWebhook{
		Investigation: EventIncomingWebhookInvestigation{Labels: map[string]string{"app_id": "checkout"}},
	}
	applyLabelMapping(&event, mapping, newLabelExtractorCache(slog.Default(), mapping), true)
	assert.Equal(t, "CHECKOUT", event.EventSubjectName)
}
