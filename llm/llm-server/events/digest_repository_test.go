package events

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDigestMarshalsJsonbAsObjects guards the jsonb columns against being typed
// as []byte. encoding/json base64-encodes a []byte field, which shipped metrics
// and class_summaries to the UI as opaque strings — the stat tiles read
// undefined and rendered zeros, and the findings list stayed empty.
func TestDigestMarshalsJsonbAsObjects(t *testing.T) {
	d := Digest{
		Metrics:        json.RawMessage(`{"analyses":598,"p1_pct":76}`),
		TopClasses:     json.RawMessage(`[{"aggregation_key":"image_pull_backoff"}]`),
		ClassSummaries: json.RawMessage(`[{"aggregation_key":"image_pull_backoff","finding":"x"}]`),
		Status:         DigestStatusGenerated,
	}

	raw, err := json.Marshal(d)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))

	metrics, ok := out["metrics"].(map[string]any)
	require.True(t, ok, "metrics must marshal as an object, got %T", out["metrics"])
	assert.EqualValues(t, 598, metrics["analyses"])

	summaries, ok := out["class_summaries"].([]any)
	require.True(t, ok, "class_summaries must marshal as an array, got %T", out["class_summaries"])
	assert.Len(t, summaries, 1)

	_, ok = out["top_classes"].([]any)
	assert.True(t, ok, "top_classes must marshal as an array, got %T", out["top_classes"])
}
