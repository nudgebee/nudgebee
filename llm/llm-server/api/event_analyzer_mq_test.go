package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The EVENT_AUTO_AI_SUMMARY kill switch must never reach a workflow-initiated
// investigation. When it did, processTroubleshootingEventFromMq set
// skipPublish and emitted a token-less envelope, runbook-server logged
// "investigation completion: empty task_token, dropping", and every
// llm.event_investigate node on an account with the flag off timed out on its
// StartToCloseTimeout.
func TestAutoAnalysisGateApplies(t *testing.T) {
	tests := []struct {
		name      string
		taskToken string
		want      bool
	}{
		{
			name:      "auto path (no token) is gated by the kill switch",
			taskToken: "",
			want:      true,
		},
		{
			name:      "workflow-initiated request bypasses the kill switch",
			taskToken: "dGFzay10b2tlbi1ieXRlcw==",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, autoAnalysisGateApplies(tt.taskToken))
		})
	}
}
