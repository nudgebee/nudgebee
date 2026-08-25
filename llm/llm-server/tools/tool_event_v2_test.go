package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/events"
)

func TestCapInsight_TruncatesOversizedData(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: strings.Repeat("x", maxEvidenceInsightDataChars+500)}

	capped := capInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
	assert.Contains(t, dataStr, "... (truncated)")
}

func TestCapInsight_LeavesSmallDataUntouched(t *testing.T) {
	insight := events.InvestigateDataInsight{Data: "small"}

	capped := capInsight(insight)

	assert.Equal(t, "small", capped.Data)
}

func TestTruncateAtRuneBoundary_NonPositiveMaxBytesReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", truncateAtRuneBoundary("hello", 0))
	assert.Equal(t, "", truncateAtRuneBoundary("hello", -1))
}

func TestCapInsight_DoesNotSplitMultiByteRune(t *testing.T) {
	// "日" is 3 bytes; place one right at the maxEvidenceInsightDataChars boundary
	// so a naive byte-index slice would cut it in half.
	data := strings.Repeat("x", maxEvidenceInsightDataChars-1) + "日本語"
	insight := events.InvestigateDataInsight{Data: data}

	capped := capInsight(insight)

	dataStr, ok := capped.Data.(string)
	assert.True(t, ok)
	truncated := strings.TrimSuffix(dataStr, "\n... (truncated)")
	assert.True(t, utf8.ValidString(truncated), "truncated string must not split a UTF-8 rune")
}

func TestCapInsightSlice_CapsEntryCountAndSize(t *testing.T) {
	insights := make([]events.InvestigateDataInsight, maxEvidenceInsightEntries+10)
	for i := range insights {
		insights[i] = events.InvestigateDataInsight{Data: strings.Repeat("y", maxEvidenceInsightDataChars+100)}
	}

	capped := capInsightSlice(insights)

	assert.Len(t, capped, maxEvidenceInsightEntries)
	for _, insight := range capped {
		dataStr, ok := insight.Data.(string)
		assert.True(t, ok)
		assert.LessOrEqual(t, len(dataStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
	}
}

func TestCapInvestigateDataEvidence_BoundsHeavyEvent(t *testing.T) {
	heavy := events.InvestigateData{
		PodEvents:  make([]events.InvestigateDataInsight, 200),
		NodeEvents: make([]events.InvestigateDataInsight, 200),
	}
	for i := range heavy.PodEvents {
		heavy.PodEvents[i] = events.InvestigateDataInsight{Data: strings.Repeat("crash-loop pod event\n", 500)}
	}
	for i := range heavy.NodeEvents {
		heavy.NodeEvents[i] = events.InvestigateDataInsight{Data: strings.Repeat("node pressure event\n", 500)}
	}
	heavy.AlertLabels = events.InvestigateDataInsight{Data: strings.Repeat("label=value;", 10000)}
	heavy.Others = make([]events.InvestigateDataInsight, 50)
	for i := range heavy.Others {
		heavy.Others[i] = events.InvestigateDataInsight{Data: strings.Repeat("other evidence\n", 500)}
	}

	capInvestigateDataEvidence(&heavy)

	assert.Len(t, heavy.PodEvents, maxEvidenceInsightEntries)
	assert.Len(t, heavy.NodeEvents, maxEvidenceInsightEntries)
	assert.Len(t, heavy.Others, maxEvidenceInsightEntries)
	labelStr, ok := heavy.AlertLabels.Data.(string)
	assert.True(t, ok)
	assert.LessOrEqual(t, len(labelStr), maxEvidenceInsightDataChars+len("\n... (truncated)"))
}
