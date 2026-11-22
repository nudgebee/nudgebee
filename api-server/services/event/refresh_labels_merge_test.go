package event

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"nudgebee/services/triage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A refresh read the labels before its playbooks ran; it writes back only what it changed, and never the
// remediation loop's resolve or re-fire labels.
func TestRefreshedLabelsPatch(t *testing.T) {
	before := map[string]string{"alertname": "HighCPU", "instance": "n1", triage.LabelResolvedBy: `{"run_id":"stale"}`}
	after := map[string]string{"alertname": "HighCPU", "instance": "n2", "pod": "api-1", triage.LabelResolvedBy: `{"run_id":"stale"}`,
		triage.LabelRefire: `{"kind":"unknown"}`}
	assert.Equal(t, map[string]string{"instance": "n2", "pod": "api-1"}, refreshedLabelsPatch(before, after))
	assert.Empty(t, refreshedLabelsPatch(before, before), "nothing changed, nothing written")
}

// The write merges into the row as it is now, so a label set while the refresh ran survives.
func TestRefreshLabelsUpdateMergesIntoTheCurrentRow(t *testing.T) {
	assert.Contains(t, refreshLabelsUpdateSQL, "labels = (CASE WHEN jsonb_typeof(labels) = 'object' THEN labels ELSE '{}'::jsonb END) || $2::jsonb")
}

// No statement in this package overwrites an event's whole label map from a value read earlier: that is how
// nb_resolved_by / nb_refire were lost. Merge (labels || patch) instead.
func TestNoWholeLabelMapOverwrite(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	overwrite := regexp.MustCompile(`(?i)\blabels\s*=\s*\$\d`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		assert.Empty(t, overwrite.FindAllString(string(src), -1), "%s overwrites events.labels with a stale map", f)
	}
}

// A label a playbook adds during a refresh is written back. The playbook writes into the refresh's own label
// map, so the snapshot must be taken before it runs; taken after, the added label is already in the snapshot
// and the patch drops it.
func TestRefreshWritesPlaybookAddedLabels(t *testing.T) {
	labels := map[string]string{"alertname": "HighCPU"}
	before := maps.Clone(labels)
	labels["pod"] = "api-1" // what eventrule.ExecutePlaybook does to PlaybookEvent.Labels
	assert.Equal(t, map[string]string{"pod": "api-1"}, refreshedLabelsPatch(before, labels))

	src, err := os.ReadFile("service.go")
	require.NoError(t, err)
	body := string(src)
	start := strings.Index(body, "func RefreshInvestigation(")
	require.GreaterOrEqual(t, start, 0)
	body = body[start:]
	snapshot := strings.Index(body, "labelsBefore := maps.Clone(labels)")
	playbook := strings.Index(body, "eventrule.ExecutePlaybook(")
	require.GreaterOrEqual(t, snapshot, 0, "RefreshInvestigation snapshots its labels")
	require.GreaterOrEqual(t, playbook, 0, "RefreshInvestigation runs the playbook")
	assert.Less(t, snapshot, playbook, "the label snapshot is taken before the playbook writes into the map")
}
