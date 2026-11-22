package event

import (
	"testing"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
)

// The SQL in applyEnrichment compares each stored card's action name against this
// list with `coalesce(elem->'additional_info'->>'action_name', ”) <> ALL($1)`. An
// empty string in the list therefore matches every card that has no action name
// and deletes it. Nothing else guards that, so this test is the guard.
func TestRegeneratedActionNames_NeverYieldsEmptyString(t *testing.T) {
	evidences := []any{
		map[string]any{"additional_info": map[string]any{"action_name": ""}},
		map[string]any{"additional_info": map[string]any{"action_name": "cloud_logs"}},
		map[string]any{"additional_info": map[string]any{"actual_action_name": ""}},
	}

	names := regeneratedActionNames(evidences)

	assert.Equal(t, []string{"cloud_logs"}, names)
	for _, n := range names {
		assert.NotEmpty(t, n, "an empty action name would wipe unrelated evidence cards")
	}
}

func TestRegeneratedActionNames_DedupesAndCollectsBothKeys(t *testing.T) {
	evidences := []any{
		map[string]any{"additional_info": map[string]any{"action_name": "cloud_logs"}},
		map[string]any{"additional_info": map[string]any{"action_name": "cloud_logs"}},
		map[string]any{"additional_info": map[string]any{
			"action_name":        "prometheus_enricher",
			"actual_action_name": "prometheus_enricher_2",
		}},
	}

	names := regeneratedActionNames(evidences)

	assert.ElementsMatch(t, []string{"cloud_logs", "prometheus_enricher", "prometheus_enricher_2"}, names)
}

func TestRegeneratedActionNames_IgnoresMalformedCards(t *testing.T) {
	evidences := []any{
		"not a map",
		map[string]any{},
		map[string]any{"additional_info": "not a map"},
		map[string]any{"additional_info": map[string]any{"action_name": 42}},
		nil,
	}

	assert.Empty(t, regeneratedActionNames(evidences))
}

// Every column read here is nullable, and this runs on a queue consumer where an
// unchecked deref would take the whole process down rather than fail one request.
func TestEventForEnrichment_NilFieldsDoNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := eventForEnrichment(models.Event{
			CloudAccountId: strPtr("acct-1"),
			Tenant:         strPtr("tenant-1"),
			// everything else nil: source, subject_name, labels, evidences, ...
		})
		assert.NoError(t, err)
	})
}

func TestEventForEnrichment_SkipsNonStringLabels(t *testing.T) {
	labels := &models.Json{}
	err := labels.UnmarshalJSON([]byte(`{"pod":"api-1","replicas":3,"ready":true}`))
	assert.NoError(t, err)

	got, err := eventForEnrichment(models.Event{
		CloudAccountId: strPtr("acct-1"),
		Tenant:         strPtr("tenant-1"),
		Labels:         labels,
	})

	assert.NoError(t, err)
	assert.Equal(t, map[string]string{"pod": "api-1"}, got.Labels,
		"non-string label values must be skipped, not asserted into a string")
}

func TestEventForEnrichment_RequiresAccountAndTenant(t *testing.T) {
	_, err := eventForEnrichment(models.Event{Tenant: strPtr("tenant-1")})
	assert.Error(t, err, "a missing cloud_account_id must be reported, not defaulted")

	_, err = eventForEnrichment(models.Event{CloudAccountId: strPtr("acct-1")})
	assert.Error(t, err, "a missing tenant must be reported, not defaulted")
}

func TestEventForEnrichment_CarriesPlaybookInputs(t *testing.T) {
	startsAt := time.Now().Add(-time.Hour)

	got, err := eventForEnrichment(models.Event{
		CloudAccountId:   strPtr("acct-1"),
		Tenant:           strPtr("tenant-1"),
		Source:           strPtr("AWS_CloudWatch_Alarm"),
		AggregationKey:   strPtr("HighCPU"),
		SubjectName:      strPtr("i-123"),
		SubjectType:      strPtr("instance"),
		SubjectNamespace: strPtr("default"),
		SubjectOwner:     strPtr("asg-1"),
		SubjectOwnerKind: strPtr("AutoScalingGroup"),
		Cluster:          strPtr("prod"),
		StartsAt:         &startsAt,
	})

	assert.NoError(t, err)
	assert.Equal(t, "AWS_CloudWatch_Alarm", got.Source)
	assert.Equal(t, "HighCPU", got.AggregationKey)
	assert.Equal(t, "i-123", got.SubjectName)
	assert.Equal(t, "asg-1", got.SubjectOwner)
	assert.Equal(t, "prod", got.Cluster)
	assert.Equal(t, &startsAt, got.StartsAt)
}

// These fallbacks used to run after the playbook, guarded by a snapshot of the
// pre-playbook label keys. They now run before it, which is what lets the row
// commit with subject_name already filled.
func TestApplySubjectFallbacks(t *testing.T) {
	t.Run("fills empty fields from labels", func(t *testing.T) {
		e := Event{Labels: map[string]string{
			"cluster":   "prod",
			"namespace": "payments",
			"pod":       "api-7",
		}}

		applySubjectFallbacks(&e)

		assert.Equal(t, "prod", e.Cluster)
		assert.Equal(t, "payments", e.SubjectNamespace)
		assert.Equal(t, "api-7", e.SubjectName)
	})

	t.Run("never overwrites a value the event already carries", func(t *testing.T) {
		e := Event{
			Cluster:          "explicit-cluster",
			SubjectNamespace: "explicit-ns",
			SubjectName:      "explicit-name",
			Labels:           map[string]string{"cluster": "prod", "namespace": "payments", "pod": "api-7"},
		}

		applySubjectFallbacks(&e)

		assert.Equal(t, "explicit-cluster", e.Cluster)
		assert.Equal(t, "explicit-ns", e.SubjectNamespace)
		assert.Equal(t, "explicit-name", e.SubjectName)
	})

	// A node is cluster-scoped. The `namespace` label on its alert is the
	// namespace of the exporter pod that reported it.
	t.Run("a node subject takes no namespace from its labels", func(t *testing.T) {
		e := Event{
			SubjectType: "Node",
			SubjectName: "gke-pool-1-node-7f3a",
			Labels:      map[string]string{"cluster": "prod", "namespace": "monitoring", "pod": "node-exporter-skqnn"},
		}

		applySubjectFallbacks(&e)

		assert.Equal(t, "", e.SubjectNamespace)
		assert.Equal(t, "prod", e.Cluster)
		assert.Equal(t, "gke-pool-1-node-7f3a", e.SubjectName)
	})

	t.Run("initialises a nil label map", func(t *testing.T) {
		e := Event{}
		assert.NotPanics(t, func() { applySubjectFallbacks(&e) })
		assert.NotNil(t, e.Labels)
	})
}
