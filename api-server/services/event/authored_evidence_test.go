package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildAuthoredEvidenceMarkdown(t *testing.T) {
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type:     EvidenceTypeMarkdown,
		Title:    "Rollout blocked",
		Severity: "Critical",
		Content:  "  The new pods never became ready.  ",
	})
	require.NoError(t, err)

	require.Equal(t, EvidenceTypeMarkdown, evidence["type"])
	require.Equal(t, "Rollout blocked", evidence["title"])
	// The markdown card reads `data`, not `text`.
	require.Equal(t, "The new pods never became ready.", evidence["data"])
	require.Equal(t, "rollout-blocked.md", evidence["filename"])
	require.Equal(t, map[string]any{
		"actual_action_name": authoredEvidenceRenderer,
		"title":              "Rollout blocked",
		"severity":           "Critical",
	}, evidence["additional_info"])
}

func TestBuildAuthoredEvidenceHighlightsOnlyWhatTheAuthorWrote(t *testing.T) {
	// Without this, every attached card put its own title into the event's
	// insights panel and into the RCA prompt as a finding.
	withoutSummary, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type: EvidenceTypeMarkdown, Title: "Rollout blocked", Severity: "Critical", Content: "body",
	})
	require.NoError(t, err)
	require.NotContains(t, withoutSummary, "insight")

	withSummary, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type: EvidenceTypeMarkdown, Title: "Rollout blocked", Summary: "3 pods never became ready",
		Severity: "Critical", Content: "body",
	})
	require.NoError(t, err)
	require.Equal(t, []any{map[string]any{"message": "3 pods never became ready", "severity": "Critical"}}, withSummary["insight"])
}

func TestBuildAuthoredEvidenceFlattensCellsThatWouldCrashThePage(t *testing.T) {
	// A nested cell reaches the page as a React child and throws, taking the
	// event page down for everyone who opens it.
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type: EvidenceTypeTable, Headers: []string{"Pod", "Detail"},
		Rows: `[["api-7f9", {"restarts": 5}]]`,
	})
	require.NoError(t, err)

	data := evidence["data"].(map[string]any)
	require.Equal(t, []any{[]any{"api-7f9", `{"restarts":5}`}}, data["rows"])
}

func TestBuildAuthoredEvidenceDefaultsTitleAndSeverity(t *testing.T) {
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeMarkdown, Content: "body"})
	require.NoError(t, err)

	require.Equal(t, defaultEvidenceTitle, evidence["title"])
	additionalInfo := evidence["additional_info"].(map[string]any)
	require.Equal(t, defaultEvidenceSeverity, additionalInfo["severity"])
}

func TestBuildAuthoredEvidenceJsonIsStoredAsCompactText(t *testing.T) {
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type:     EvidenceTypeJson,
		Title:    "Deployment",
		JsonData: "{\n  \"replicas\": 3,\n  \"image\": \"api:1.4.2\"\n}",
	})
	require.NoError(t, err)

	require.Equal(t, EvidenceTypeJson, evidence["type"])
	// The card parses this back out of a string.
	require.Equal(t, `{"image":"api:1.4.2","replicas":3}`, evidence["data"])
	require.Equal(t, "deployment.json", evidence["filename"])
}

func TestBuildAuthoredEvidenceJsonAcceptsAnArray(t *testing.T) {
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson, JsonData: `[{"a":1}]`})
	require.NoError(t, err)
	require.Equal(t, `[{"a":1}]`, evidence["data"])
}

func TestBuildAuthoredEvidenceTable(t *testing.T) {
	evidence, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type:    EvidenceTypeTable,
		Title:   "Restarting pods",
		Headers: []string{"Pod", " Reason ", ""},
		Rows:    `[["api-7f9","CrashLoopBackOff"],["web-2b1","OOMKilled"]]`,
	})
	require.NoError(t, err)

	require.Equal(t, EvidenceTypeTable, evidence["type"])
	data, ok := evidence["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, []any{"Pod", "Reason"}, data["headers"])
	require.Equal(t, []any{
		[]any{"api-7f9", "CrashLoopBackOff"},
		[]any{"web-2b1", "OOMKilled"},
	}, data["rows"])
	// The table card calls .includes on this, so it must always be a string.
	require.Equal(t, "Restarting pods", data["table_name"])
	require.Equal(t, map[string]any{}, data["column_renderers"])
}

func TestBuildAuthoredEvidenceRejectsATypeThatCannotBeDisplayed(t *testing.T) {
	_, err := BuildAuthoredEvidence(AuthoredEvidence{Type: "file", Content: "x"})
	require.ErrorContains(t, err, `"file" is not an evidence type that can be displayed`)
	require.ErrorContains(t, err, "markdown, json, table")
}

func TestBuildAuthoredEvidenceRequiresTheFieldsItsTypeUses(t *testing.T) {
	_, err := BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeMarkdown})
	require.ErrorContains(t, err, "content is required")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson})
	require.ErrorContains(t, err, "json_data is required")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeTable, Rows: `[["a"]]`})
	require.ErrorContains(t, err, "headers are required")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeTable, Headers: []string{"Pod"}})
	require.ErrorContains(t, err, "rows are required")
}

func TestBuildAuthoredEvidenceRejectsJsonTheCardCannotRender(t *testing.T) {
	_, err := BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson, JsonData: "{not json"})
	require.ErrorContains(t, err, "not valid JSON")

	// A bare scalar has no entries, so it would draw an empty card.
	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson, JsonData: `"just a string"`})
	require.ErrorContains(t, err, "must be a JSON object or array")

	// So would an empty one — the table type already refuses its own empty case.
	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson, JsonData: `{}`})
	require.ErrorContains(t, err, "json_data must not be empty")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeJson, JsonData: `[]`})
	require.ErrorContains(t, err, "json_data must not be empty")
}

func TestBuildAuthoredEvidenceRejectsRowsThatAreNotArrays(t *testing.T) {
	// The table card destructures each row, so an object row throws in the browser.
	_, err := BuildAuthoredEvidence(AuthoredEvidence{
		Type: EvidenceTypeTable, Headers: []string{"Pod"}, Rows: `[{"pod":"api-7f9"}]`,
	})
	require.ErrorContains(t, err, "row 1 must be an array of cells")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeTable, Headers: []string{"Pod"}, Rows: `{"pod":"a"}`})
	require.ErrorContains(t, err, "rows must be a JSON array of arrays")

	_, err = BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeTable, Headers: []string{"Pod"}, Rows: `[]`})
	require.ErrorContains(t, err, "rows must not be empty")
}

func TestBuildAuthoredEvidenceRejectsAnUnknownSeverity(t *testing.T) {
	_, err := BuildAuthoredEvidence(AuthoredEvidence{Type: EvidenceTypeMarkdown, Content: "x", Severity: "Urgent"})
	require.ErrorContains(t, err, "severity must be one of: Info, High, Critical")
}
