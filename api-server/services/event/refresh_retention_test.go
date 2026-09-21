package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var refreshSourceActions = map[string]bool{"cloud_logs": true}

func TestRetainEvidenceKeepsAutomationCardsWithoutARunToStampThem(t *testing.T) {
	// The builder's Run Task executes one task with no workflow identity, so its
	// card carries no source_workflow — only the renderer key. Keying on the
	// stamp alone deleted those on the next refresh, minutes after the author
	// watched the card appear.
	card := map[string]any{
		"type":            "markdown",
		"additional_info": map[string]any{"actual_action_name": authoredEvidenceRenderer},
	}
	require.True(t, retainEvidenceOnRefresh(card, map[string]bool{}, refreshSourceActions))

	// Even when the playbook just produced a card under the same name.
	regenerated := map[string]bool{authoredEvidenceRenderer: true}
	require.True(t, retainEvidenceOnRefresh(card, regenerated, refreshSourceActions))
}

func TestRetainEvidenceKeepsStampedAutomationCards(t *testing.T) {
	card := map[string]any{
		"additional_info": map[string]any{
			"action_name":             "text_enricher",
			evidenceSourceWorkflowKey: map[string]any{"workflow_id": "wf-1"},
		},
	}
	require.True(t, retainEvidenceOnRefresh(card, map[string]bool{"text_enricher": true}, refreshSourceActions))
}

func TestRetainEvidenceDropsAnEnricherCardTheRefreshRegenerates(t *testing.T) {
	card := map[string]any{"additional_info": map[string]any{"action_name": "pod_enricher"}}
	require.False(t, retainEvidenceOnRefresh(card, map[string]bool{"pod_enricher": true}, refreshSourceActions))
}

func TestRetainEvidenceKeepsSourceAndDetailCards(t *testing.T) {
	webhook := map[string]any{"additional_info": map[string]any{"action_name": "webhook_event"}}
	require.True(t, retainEvidenceOnRefresh(webhook, map[string]bool{}, refreshSourceActions))

	cloudLogs := map[string]any{"additional_info": map[string]any{"action_name": "cloud_logs"}}
	require.True(t, retainEvidenceOnRefresh(cloudLogs, map[string]bool{}, refreshSourceActions))

	detail := map[string]any{"additional_info": map[string]any{"action_type": "event_detail"}}
	require.True(t, retainEvidenceOnRefresh(detail, map[string]bool{}, refreshSourceActions))
}

func TestRetainEvidenceDropsAnythingElse(t *testing.T) {
	require.False(t, retainEvidenceOnRefresh(map[string]any{"type": "markdown"}, map[string]bool{}, refreshSourceActions))
	require.False(t, retainEvidenceOnRefresh(
		map[string]any{"additional_info": map[string]any{"action_name": "logs_enricher"}},
		map[string]bool{}, refreshSourceActions))
}
