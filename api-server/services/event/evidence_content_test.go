package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// gzipEmpty and gzipTwoLines are verbatim agent payloads taken from stored
// k8s_pod_log_enricher evidences on dev.
const (
	gzipEmpty    = `b'H4sIAAAAAAAA/wEAAP//AAAAAAAAAAA='`
	gzipTwoLines = `b'H4sIAAAAAAAA/youSSwqycxL50pLLEnMsVJITszLyy9RSM7Py0tNLlEoyVdISeICBAAA//9rwZaxJQAAAA=='`
)

func logEvidence(action string, data any) map[string]any {
	return map[string]any{
		"type":            "file",
		"data":            data,
		"additional_info": map[string]any{"action_name": action},
	}
}

func TestEvidenceHasContent(t *testing.T) {
	tests := []struct {
		name     string
		evidence map[string]any
		want     bool
	}{
		{"agent gzip with log lines", logEvidence("k8s_pod_log_enricher", gzipTwoLines), true},
		{"agent gzip of empty log", logEvidence("k8s_pod_log_enricher", gzipEmpty), false},
		{"json wrapper with rows", logEvidence("logs", `{"data":[{"message":"boom"}]}`), true},
		{"json wrapper with no rows", logEvidence("cloud_logs", `{"data":[]}`), false},
		{"map wrapper with rows", logEvidence("logs", map[string]any{"data": []any{"x"}}), true},
		{"map wrapper with no rows", logEvidence("logs", map[string]any{"data": []any{}}), false},
		{"bare array with items", logEvidence("logs", []any{"x"}), true},
		{"bare empty array", logEvidence("logs", []any{}), false},
		{"blank string", logEvidence("logs", "   "), false},
		{"nil data", logEvidence("logs", nil), false},
		// Conservative: shapes we do not recognise keep the old suppress-the-twin behaviour.
		{"plain text", logEvidence("logs", "some output"), true},
		{"undecodable wrapper", logEvidence("logs", "b'not-base64!!'"), true},
		{"unknown shape", map[string]any{"data": 42}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, evidenceHasContent(tc.evidence))
		})
	}
}

func TestExtractEvidenceActionNamesSkipsEmptyEnrichments(t *testing.T) {
	t.Run("empty agent log evidence no longer suppresses the server-side logs action", func(t *testing.T) {
		// The whole point: k8s_pod_log_enricher is a member of eventrule.logActions, so
		// recording it here marks the entire log category as already collected and the
		// `logs` action never queries the account's configured log source.
		actions := extractEvidenceActionNames([]any{
			logEvidence("k8s_pod_log_enricher", gzipEmpty),
		})
		assert.NotContains(t, actions, "k8s_pod_log_enricher")
		assert.Empty(t, actions)
	})

	t.Run("agent log evidence with content still suppresses it", func(t *testing.T) {
		actions := extractEvidenceActionNames([]any{
			logEvidence("k8s_pod_log_enricher", gzipTwoLines),
		})
		assert.True(t, actions["k8s_pod_log_enricher"])
	})

	t.Run("actual_action_name is recorded alongside action_name", func(t *testing.T) {
		actions := extractEvidenceActionNames([]any{
			map[string]any{
				"data": "output",
				"additional_info": map[string]any{
					"action_name":        "logs",
					"actual_action_name": "signoz_logs_enricher",
				},
			},
		})
		assert.True(t, actions["logs"])
		assert.True(t, actions["signoz_logs_enricher"])
	})

	t.Run("evidence without additional_info is ignored", func(t *testing.T) {
		assert.Empty(t, extractEvidenceActionNames([]any{map[string]any{"data": "x"}, "not-a-map"}))
	})
}

func TestExtractEvidenceActionNamesOnlyGatesLogActions(t *testing.T) {
	// The content gate is deliberately limited to the log-action set, because that is
	// the only category whose suppression is all-or-nothing across actions.
	t.Run("empty non-log enrichment still suppresses its twin", func(t *testing.T) {
		actions := extractEvidenceActionNames([]any{
			logEvidence("resource_events_enricher", `{"data":[]}`),
		})
		assert.True(t, actions["resource_events_enricher"])
	})

	t.Run("empty log enrichment does not", func(t *testing.T) {
		actions := extractEvidenceActionNames([]any{
			logEvidence("logs", `{"data":[]}`),
		})
		assert.Empty(t, actions)
	})
}
