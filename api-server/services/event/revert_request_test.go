package event

import (
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

// diffEvidence builds the `type: "diff"` evidence block the k8s agents attach
// to a config-change event, with the updated_values shape the revert path reads.
func diffEvidence(updatedValues []any) *models.Json {
	j := models.NewJsonArray([]any{
		map[string]any{"type": "table", "data": map[string]any{"table_name": "*Alert labels*"}},
		map[string]any{
			"type": "diff",
			"data": map[string]any{
				"resource_name":  "deployment/shop/services-server.yaml",
				"old":            "metadata:\n  name: services-server\n",
				"new":            "metadata:\n  name: services-server\n",
				"updated_values": updatedValues,
			},
		},
	})
	return &j
}

func revertEvent(updatedValues []any) models.Event {
	return models.Event{
		Id:               "evt-1",
		SubjectNamespace: strPtr("shop"),
		Evidences:        diffEvidence(updatedValues),
	}
}

var deploymentResource = models.Resource{ResourceId: strPtr("shop/Deployment/services-server")}

// The request must carry the changed field paths, NOT the evidence's
// pre-change manifest: that manifest is snake_case (Hikaru wire compat), and
// replaying it through the agent's dynamic client made the apiserver drop every
// renamed key and reject the remains with "spec.selector: empty selector is
// invalid for deployment".
func TestGetRevertRecommendationRequest_SendsPathsNotManifest(t *testing.T) {
	req, err := getRevertRecommendationRequest(deploymentResource, revertEvent([]any{
		map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": "zde7X", "new": "c4cjS"},
		map[string]any{"path": "spec.template.spec.containers[0].image", "old": "registry/app:old", "new": "registry/app:new"},
	}), false)
	require.NoError(t, err)

	assert.Equal(t, "services-server", req["name"])
	assert.Equal(t, "Deployment", req["kind"])
	assert.Equal(t, strPtr("shop"), req["namespace"])
	assert.Equal(t, []any{
		map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": "zde7X"},
		map[string]any{"path": "spec.template.spec.containers[0].image", "old": "registry/app:old"},
	}, req["revert_paths"])

	// The snake_case manifest still rides along for tenants on the Python agent,
	// which is the only consumer that can read it.
	assert.Contains(t, req, "deployment")
}

// A tenant still on the Python agent gets served by its replace_workload, which
// reads the per-kind manifest and ignores unknown params — so the manifest must
// keep going out even once revert_paths is the shape that matters.
func TestGetRevertRecommendationRequest_KeepsLegacyManifestForPythonAgent(t *testing.T) {
	req, err := getRevertRecommendationRequest(deploymentResource, revertEvent([]any{
		map[string]any{"path": "spec.replicas", "old": float64(2)},
	}), false)
	require.NoError(t, err)
	require.Contains(t, req, "deployment", "per-kind manifest missing: %#v", req)

	// Assert on the marshalled payload, not the Go types: yaml.v2 decodes nested
	// maps as map[interface{}]interface{}, which encoding/json refuses outright.
	// It only reaches the agent because common.MarshalJson is jsoniter. This
	// assertion is the tripwire if that ever changes.
	payload, err := common.MarshalJson(req)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"name": "services-server",
		"namespace": "shop",
		"kind": "Deployment",
		"revert_paths": [{"path": "spec.replicas", "old": 2}],
		"deployment": {"metadata": {"name": "services-server"}}
	}`, string(payload))
}

// Without updated_values the paths can't be built, but the manifest alone still
// lets a Python-agent tenant revert, so the request must not be refused.
func TestGetRevertRecommendationRequest_ManifestOnly(t *testing.T) {
	req, err := getRevertRecommendationRequest(deploymentResource, revertEvent(nil), false)
	require.NoError(t, err)
	assert.NotContains(t, req, "revert_paths")
	assert.Contains(t, req, "deployment")
}

// `old: null` means the change ADDED the field. It has to survive to the agent
// as a null so the agent removes the field rather than writing a null into it.
func TestGetRevertRecommendationRequest_KeepsNullOld(t *testing.T) {
	req, err := getRevertRecommendationRequest(deploymentResource, revertEvent([]any{
		map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": nil, "new": "c4cjS"},
	}), false)
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": nil}}, req["revert_paths"])
}

// Previously this returned an empty params map with a nil error, so the agent
// got a bodyless replace_workload and failed with an opaque "name required".
func TestGetRevertRecommendationRequest_NoDiffEvidence(t *testing.T) {
	empty := models.NewJsonArray([]any{map[string]any{"type": "table", "data": map[string]any{}}})
	_, err := getRevertRecommendationRequest(deploymentResource, models.Event{
		Id:               "evt-1",
		SubjectNamespace: strPtr("shop"),
		Evidences:        &empty,
	}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no recorded configuration change")
}

func TestGetRevertRecommendationRequest_Rejects(t *testing.T) {
	t.Run("no cloud resource", func(t *testing.T) {
		_, err := getRevertRecommendationRequest(models.Resource{}, revertEvent(nil), false)
		require.Error(t, err)
	})
	t.Run("malformed resource id", func(t *testing.T) {
		_, err := getRevertRecommendationRequest(models.Resource{ResourceId: strPtr("services-server")}, revertEvent(nil), false)
		require.Error(t, err)
	})
	t.Run("nil evidences", func(t *testing.T) {
		_, err := getRevertRecommendationRequest(deploymentResource, models.Event{Id: "evt-1"}, false)
		require.Error(t, err)
	})
	t.Run("diff evidence with neither paths nor manifest", func(t *testing.T) {
		bare := models.NewJsonArray([]any{map[string]any{"type": "diff", "data": map[string]any{}}})
		_, err := getRevertRecommendationRequest(deploymentResource, models.Event{Id: "evt-1", Evidences: &bare}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no recorded configuration change")
	})
	// An OOM alert with five correlated ConfigMap edits attached: with no change selected, the
	// paths of every edit were merged and written onto the workload the alert is about.
	t.Run("several correlated changes and none selected", func(t *testing.T) {
		cm := func(ts string) map[string]any {
			return map[string]any{"type": "diff", "data": map[string]any{
				"resource_name":  "configmap/shop/rulefiles-0.yaml",
				"updated_values": []any{map[string]any{"path": "data.rules", "old": ts, "new": "x"}},
			}}
		}
		several := models.NewJsonArray([]any{cm("06:28"), cm("06:26")})
		_, err := getRevertRecommendationRequest(deploymentResource, models.Event{Id: "evt-1", Evidences: &several}, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "select the change to revert")
	})
}

func TestExtractRevertPaths_SkipsMalformedEntries(t *testing.T) {
	paths := extractRevertPaths(revertEvent([]any{
		map[string]any{"path": "spec.replicas", "old": float64(2)},
		map[string]any{"old": "orphan"}, // no path
		"not-an-object",
	}), false)
	assert.Equal(t, []any{map[string]any{"path": "spec.replicas", "old": float64(2)}}, paths)
}

// Undoing a revert is the same operation with the diff read the other way round. One builder serves
// both directions, so the undo path cannot drift from the revert path that runs every day.
func TestGetRevertRecommendationRequest_UndoReappliesTheChange(t *testing.T) {
	event := revertEvent([]any{
		map[string]any{"path": "spec.template.spec.containers[0].image", "old": "app:v1", "new": "app:v2"},
	})

	revert, err := getRevertRecommendationRequest(deploymentResource, event, false)
	require.NoError(t, err)
	assert.Equal(t,
		[]any{map[string]any{"path": "spec.template.spec.containers[0].image", "old": "app:v1"}},
		revert["revert_paths"], "revert writes the value from before the change")

	undo, err := getRevertRecommendationRequest(deploymentResource, event, true)
	require.NoError(t, err)
	assert.Equal(t,
		[]any{map[string]any{"path": "spec.template.spec.containers[0].image", "old": "app:v2"}},
		undo["revert_paths"], "undo writes the value the change introduced")
}

// A field the change ADDED has old=null, so reverting deletes it. Undoing that must put it back
// rather than delete it again — the asymmetry that makes a naive "re-run it" undo wrong.
func TestGetRevertRecommendationRequest_UndoRestoresAnAddedField(t *testing.T) {
	event := revertEvent([]any{
		map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": nil, "new": "c4cjS"},
	})
	undo, err := getRevertRecommendationRequest(deploymentResource, event, true)
	require.NoError(t, err)
	assert.Equal(t,
		[]any{map[string]any{"path": "spec.template.metadata.annotations.rollme", "old": "c4cjS"}},
		undo["revert_paths"])
}

// changedValuesOf feeds the conflict check, so it must read the same diff evidence the revert
// itself is built from — a disagreement between the two would let a conflicting change through.
func TestChangedValuesOf(t *testing.T) {
	e := revertEvent([]any{
		map[string]any{"path": "spec.template.spec.containers[0].image", "old": "v1", "new": "v2"},
		map[string]any{"path": "spec.replicas", "old": float64(3), "new": float64(5)},
	})

	values := changedValuesOf(e)
	assert.Equal(t, "v2", values["spec.template.spec.containers[0].image"], "the value the change LEFT is what a later change is compared against")
	assert.Equal(t, float64(5), values["spec.replicas"])

	assert.Empty(t, changedValuesOf(models.Event{Id: "evt-2"}), "an event with no evidence touches no paths")
}

// Dev records the same config change twice: undo-test-app carries pairs of events sharing a
// timestamp, a path and both values. With `starts_at >=` catching same-instant changes, a twin
// looks exactly like a later change on the same path — and refusing a safe revert is worse than
// the gap that predicate was widened to close. The values are what tell them apart.
func TestSameChangedValue(t *testing.T) {
	assert.True(t, sameChangedValue("busybox:1.36", "busybox:1.36"), "a duplicate record is not a conflict")
	assert.False(t, sameChangedValue("busybox:1.36", "busybox:1.37"), "a different value IS a conflict")

	cmd := []any{"/bin/sh", "-c", "echo boom; exit 1"}
	assert.True(t, sameChangedValue(cmd, []any{"/bin/sh", "-c", "echo boom; exit 1"}))
	assert.False(t, sameChangedValue(cmd, []any{"/bin/sh", "-c", "echo ok"}))

	// nil means the field was absent on that side; absent equals absent, and never equals a value.
	assert.True(t, sameChangedValue(nil, nil))
	assert.False(t, sameChangedValue(nil, "x"))
	assert.False(t, sameChangedValue("x", nil))
}

func TestSameChangeTarget(t *testing.T) {
	resource := "ns/Deployment/api"
	other := "ns/Deployment/worker"
	key := "ns/Deployment/api"

	assert.True(t, sameChangeTarget(
		models.Event{CloudResourceId: &resource},
		models.Event{CloudResourceId: &resource},
	))
	assert.False(t, sameChangeTarget(
		models.Event{CloudResourceId: &resource},
		models.Event{CloudResourceId: &other},
	), "a change on another workload must never be revertible from this event")

	// cloud_resource_id is preferred, but an event recorded before its resource synced has only
	// service_key — the same fallback the resource lookup uses.
	assert.True(t, sameChangeTarget(
		models.Event{ServiceKey: &key},
		models.Event{ServiceKey: &key},
	))
	assert.False(t, sameChangeTarget(models.Event{}, models.Event{}),
		"two events that identify no resource are not therefore the same resource")
}

func TestSameStringValue(t *testing.T) {
	a, b := "x", "x"
	c := "y"
	assert.True(t, sameStringValue(&a, &b))
	assert.False(t, sameStringValue(&a, &c))
	// A nil tenant must not compare equal to anything: it is missing, not a wildcard.
	assert.False(t, sameStringValue(nil, &a))
	assert.False(t, sameStringValue(&a, nil))
	assert.False(t, sameStringValue(nil, nil))
}

func TestHasManifestPayload(t *testing.T) {
	// The builder's fixed fields alone are not a manifest.
	assert.False(t, hasManifestPayload(map[string]any{
		"name": "services-server", "namespace": "shop", "kind": "Deployment",
	}))
	assert.False(t, hasManifestPayload(map[string]any{
		"name": "services-server", "namespace": "shop", "kind": "Deployment",
		"revert_paths": []any{map[string]any{"path": "spec.replicas", "old": 3}},
	}))

	// The per-kind key is whatever the kind lowercases to, so the test is "anything else present".
	for _, kind := range []string{"deployment", "statefulset", "daemonset", "rollout"} {
		assert.True(t, hasManifestPayload(map[string]any{
			"name": "services-server", "namespace": "shop", "kind": "Deployment",
			kind: map[string]any{"spec": map[string]any{}},
		}), kind)
	}

	// A manifest alongside paths is the compatibility shape sent to un-migrated tenants; the
	// path-based check runs for it, so this only has to not crash.
	assert.True(t, hasManifestPayload(map[string]any{
		"name": "services-server", "namespace": "shop", "kind": "Deployment",
		"revert_paths": []any{}, "deployment": map[string]any{},
	}))
}

// Recorded paths address list items by position, and the agent writes a revert at that position.
// A later change conflicts when it touched the same field, a parent or child of it, or added or
// removed an item at or before its position in the same list — then the position names a
// different item, and the revert would overwrite the wrong entry.
func TestLaterChangeConflict(t *testing.T) {
	change := func(id string, values ...map[string]any) models.Event {
		updated := make([]any, 0, len(values))
		for _, v := range values {
			updated = append(updated, v)
		}
		e := revertEvent(updated)
		e.Id = id
		return e
	}
	uv := func(path string, old, new any) map[string]any {
		return map[string]any{"path": path, "old": old, "new": new}
	}
	envTwo := "spec.template.spec.containers[0].env[2]"
	target := map[string]bool{envTwo: true}
	chosen := change("chosen", uv(envTwo, nil, map[string]any{"name": "LLM_SERVER_URL", "value": "http://llm"}))

	cases := []struct {
		name     string
		later    models.Event
		conflict bool
	}{
		{"unrelated field", change("l", uv("spec.template.spec.containers[0].image", "v1", "v2")), false},
		{"same path, new value", change("l", uv(envTwo, map[string]any{"name": "LLM_SERVER_URL"}, map[string]any{"name": "OTHER"})), true},
		{"same change recorded twice", change("l", uv(envTwo, nil, map[string]any{"name": "LLM_SERVER_URL", "value": "http://llm"})), false},
		{"child of the path", change("l", uv(envTwo+".value", "http://llm", "http://llm-2")), true},
		{"parent of the path", change("l", uv("spec.template.spec.containers[0].env", []any{}, []any{map[string]any{"name": "A"}})), true},
		{"item removed before it in the same list", change("l", uv("spec.template.spec.containers[0].env[1]", map[string]any{"name": "A"}, nil)), true},
		{"item added at its position", change("l", uv("spec.template.spec.containers[0].env[2]", nil, map[string]any{"name": "B"})), true},
		{"item added after it", change("l", uv("spec.template.spec.containers[0].env[3]", nil, map[string]any{"name": "B"})), false},
		{"field inside an earlier item edited in place", change("l", uv("spec.template.spec.containers[0].env[1].value", "a", "b")), false},
		{"container added before its container", change("l", uv("spec.template.spec.containers[0]", nil, map[string]any{"name": "sidecar"})), true},
		{"container added after its container", change("l", uv("spec.template.spec.containers[1]", nil, map[string]any{"name": "sidecar"})), false},
		{"same index in another container's list", change("l", uv("spec.template.spec.containers[1].env[0]", map[string]any{"name": "A"}, nil)), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := laterChangeConflict(chosen, target, []models.Event{tc.later}, false)
			if tc.conflict {
				assert.NotEmpty(t, reason)
			} else {
				assert.Empty(t, reason)
			}
		})
	}
}

func TestShiftsListPosition(t *testing.T) {
	assert.True(t, shiftsListPosition("spec.containers[0].env[1]", "spec.containers[0].env[2].value"))
	assert.True(t, shiftsListPosition("spec.containers[0]", "spec.containers[1].image"))
	assert.False(t, shiftsListPosition("spec.containers[0].env[3]", "spec.containers[0].env[2]"))
	assert.False(t, shiftsListPosition("spec.containers[0].env", "spec.containers[0].env[2]"), "a whole list is a parent, not an item")
	assert.False(t, shiftsListPosition("spec.containers[1].env[0]", "spec.containers[0].env[2]"))
}

// cubeapm: the history carried a ConfigMap change filed under the StatefulSet's resource, so the
// same-resource check passed and the revert would have written `data.config.properties` onto the
// StatefulSet. A change to any object that is not the workload must be refused.
func TestGetRevertRecommendationRequest_RefusesAnotherObjectsChange(t *testing.T) {
	withResourceName := func(resourceName string, updated []any) models.Event {
		j := models.NewJsonArray([]any{map[string]any{
			"type": "diff",
			"data": map[string]any{"resource_name": resourceName, "old": "a: 1\n", "new": "a: 2\n", "updated_values": updated},
		}})
		return models.Event{Id: "evt-1", SubjectNamespace: strPtr("shop"), Evidences: &j}
	}
	dataPath := []any{map[string]any{"path": "data.config.properties", "old": "x", "new": "y"}}
	imagePath := []any{map[string]any{"path": "spec.template.spec.containers[0].image", "old": "v1", "new": "v2"}}

	_, err := getRevertRecommendationRequest(deploymentResource, withResourceName("configmap/shop/services-server.yaml", dataPath), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "configmap")

	_, err = getRevertRecommendationRequest(deploymentResource, revertEvent(dataPath), false)
	require.NoError(t, err, "revertEvent's diff names a deployment, so its kind decides, not the paths")

	noName := models.NewJsonArray([]any{map[string]any{"type": "diff", "data": map[string]any{"updated_values": dataPath}}})
	_, err = getRevertRecommendationRequest(deploymentResource, models.Event{Id: "evt-2", SubjectNamespace: strPtr("shop"), Evidences: &noName}, false)
	require.Error(t, err, "without a resource_name, data-only paths identify a ConfigMap or Secret")

	_, err = getRevertRecommendationRequest(deploymentResource, withResourceName("statefulset/shop/services-server.yaml", imagePath), false)
	require.NoError(t, err)
}

// The tests below hold the rule that a later change counts only while it still stands.

const (
	imagePath  = "spec.template.spec.containers[0].image"
	rollmePath = "spec.template.metadata.annotations.rollme"
	envValue   = "spec.template.spec.containers[0].env[2].value"
	envItem    = "spec.template.spec.containers[0].env[2]"
	envBefore  = "spec.template.spec.containers[0].env[1]"
)

var changeRecordedAt = time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

func fieldChange(path string, old, new any) map[string]any {
	return map[string]any{"path": path, "old": old, "new": new}
}

// recordedChange is a change recorded `minutes` after the one under test.
func recordedChange(minutes int, values ...map[string]any) models.Event {
	updated := make([]any, 0, len(values))
	for _, v := range values {
		updated = append(updated, v)
	}
	e := revertEvent(updated)
	at := changeRecordedAt.Add(time.Duration(minutes) * time.Minute)
	e.StartsAt = &at
	return e
}

func targetsOf(change models.Event) map[string]bool {
	targeted := map[string]bool{}
	for path := range changedValuesOf(change) {
		targeted[path] = true
	}
	return targeted
}

// Before this, any later record that ever differed refused the revert for good, so the refusal's
// own advice — revert the later change first — led nowhere: the later change stayed on record.
func TestLaterChangeConflict_ALaterChangeCountsOnlyWhileItStands(t *testing.T) {
	image := recordedChange(0, fieldChange(imagePath, "v1", "v2"))
	env := recordedChange(0, fieldChange(envValue, "a", "b"))

	cases := []struct {
		name   string
		change models.Event
		later  []models.Event
		// "" when the revert is allowed; otherwise what the refusal must contain.
		refusal string
	}{
		{"changed again", image, []models.Event{recordedChange(10, fieldChange(imagePath, "v2", "v3"))}, "2026-03-04T10:10:00Z"},
		{"changed again, then put back", image, []models.Event{
			recordedChange(10, fieldChange(imagePath, "v2", "v3")),
			recordedChange(20, fieldChange(imagePath, "v3", "v2")),
		}, ""},
		{"put back, then changed once more", image, []models.Event{
			recordedChange(10, fieldChange(imagePath, "v2", "v3")),
			recordedChange(20, fieldChange(imagePath, "v3", "v2")),
			recordedChange(30, fieldChange(imagePath, "v2", "v4")),
		}, "2026-03-04T10:30:00Z"},
		// The time is the newest change's: that is the one that has to be rolled back first.
		{"two later changes stand", image, []models.Event{
			recordedChange(10, fieldChange(imagePath, "v2", "v3")),
			recordedChange(20, fieldChange(imagePath, "v3", "v4")),
		}, "2026-03-04T10:20:00Z"},
		// Two changes at one instant cannot be put in order, so which value is live is unknown.
		{"moved and put back at the same instant", image, []models.Event{
			recordedChange(10, fieldChange(imagePath, "v2", "v3")),
			recordedChange(10, fieldChange(imagePath, "v3", "v2")),
		}, "changed again"},
		{"the same, read in the other order", image, []models.Event{
			recordedChange(10, fieldChange(imagePath, "v3", "v2")),
			recordedChange(10, fieldChange(imagePath, "v2", "v3")),
		}, "changed again"},
		{"the change itself, recorded twice", image, []models.Event{recordedChange(0, fieldChange(imagePath, "v1", "v2"))}, ""},

		{"parent replaced", env, []models.Event{
			recordedChange(10, fieldChange(envItem, map[string]any{"name": "A", "value": "b"}, map[string]any{"name": "A", "value": "c"})),
		}, "changed again"},
		{"parent replaced, then put back", env, []models.Event{
			recordedChange(10, fieldChange(envItem, map[string]any{"name": "A", "value": "b"}, map[string]any{"name": "A", "value": "c"})),
			recordedChange(20, fieldChange(envItem, map[string]any{"name": "A", "value": "c"}, map[string]any{"name": "A", "value": "b"})),
		}, ""},
		{"item added ahead of it", env, []models.Event{
			recordedChange(10, fieldChange(envBefore, nil, map[string]any{"name": "NEW"})),
		}, "added or removed an item"},
		{"item added ahead of it, then removed again", env, []models.Event{
			recordedChange(10, fieldChange(envBefore, nil, map[string]any{"name": "NEW"})),
			recordedChange(20, fieldChange(envBefore, map[string]any{"name": "NEW"}, nil)),
		}, ""},
		// The item that went is not the one that came: the positions are not what they were.
		{"item added ahead of it, a different one removed", env, []models.Event{
			recordedChange(10, fieldChange(envBefore, nil, map[string]any{"name": "NEW"})),
			recordedChange(20, fieldChange("spec.template.spec.containers[0].env[0]", map[string]any{"name": "FIRST"}, nil)),
		}, "added or removed an item"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := laterChangeConflict(tc.change, targetsOf(tc.change), tc.later, false)
			if tc.refusal == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tc.refusal)
			}
		})
	}
}

// A Helm upgrade stamps a new random `rollme` on the pod template every time, and a restart stamps
// `restartedAt`. Neither is configuration and neither is ever put back, so while they counted,
// one later deploy refused every earlier rollback for good — naming the marker as the reason.
func TestLaterChangeConflict_RolloutMarkersNeverCount(t *testing.T) {
	deploy := recordedChange(0, fieldChange(rollmePath, "a", "b"), fieldChange(imagePath, "v1", "v2"))
	restartedAt := "spec.template.metadata.annotations.kubectl.kubernetes.io/restartedAt"
	annotations := "spec.template.metadata.annotations"

	cases := []struct {
		name    string
		later   []models.Event
		refusal string
	}{
		{"a later deploy of the same image", []models.Event{recordedChange(10, fieldChange(rollmePath, "b", "c"))}, ""},
		{"a later restart", []models.Event{recordedChange(10, fieldChange(restartedAt, nil, "2026-03-04T10:10:00Z"))}, ""},
		{"a later deploy of another image", []models.Event{
			recordedChange(10, fieldChange(rollmePath, "b", "c"), fieldChange(imagePath, "v2", "v3")),
		}, `"` + imagePath + `" was changed again`},
		{"another image, then that image put back by yet another deploy", []models.Event{
			recordedChange(10, fieldChange(rollmePath, "b", "c"), fieldChange(imagePath, "v2", "v3")),
			recordedChange(20, fieldChange(rollmePath, "c", "d"), fieldChange(imagePath, "v3", "v2")),
		}, ""},
		// Some records carry the annotations map whole rather than the one key.
		{"the marker recorded as a whole annotations map", []models.Event{
			recordedChange(10, fieldChange(annotations, map[string]any{"rollme": "b", "team": "x"}, map[string]any{"rollme": "c", "team": "x"})),
		}, ""},
		{"an annotations map first recorded, markers only", []models.Event{
			recordedChange(10, fieldChange(annotations, nil, map[string]any{"kubectl.kubernetes.io/restartedAt": "2026-03-04T10:10:00Z"})),
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := laterChangeConflict(deploy, targetsOf(deploy), tc.later, false)
			if tc.refusal == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tc.refusal)
			}
		})
	}

	// A real annotation changed inside that map is a change like any other.
	team := recordedChange(0, fieldChange(annotations+".team", "x", "y"))
	wholeMap := recordedChange(10, fieldChange(annotations, map[string]any{"rollme": "b", "team": "y"}, map[string]any{"rollme": "c", "team": "z"}))
	assert.Contains(t, laterChangeConflict(team, targetsOf(team), []models.Event{wholeMap}, false), "changed again")

	// And when the change being rolled back is itself recorded as the whole map, the rollback writes
	// the whole map back — every annotation in it, not only the marker that moved. A later edit to a
	// real annotation, or one added since, would be overwritten or dropped, so it must refuse; a
	// later marker alone still does not.
	mapChange := recordedChange(0, fieldChange(annotations, map[string]any{"rollme": "a", "team": "x"}, map[string]any{"rollme": "b", "team": "x"}))
	for name, later := range map[string]models.Event{
		"a real annotation edited in the map": recordedChange(10, fieldChange(annotations, map[string]any{"rollme": "b", "team": "x"}, map[string]any{"rollme": "b", "team": "y"})),
		"a real annotation added by its key":  recordedChange(10, fieldChange(annotations+".owner", nil, "bob")),
	} {
		assert.Contains(t, laterChangeConflict(mapChange, targetsOf(mapChange), []models.Event{later}, false), "changed again", name)
	}
	laterMarker := recordedChange(10, fieldChange(rollmePath, "b", "c"))
	assert.Empty(t, laterChangeConflict(mapChange, targetsOf(mapChange), []models.Event{laterMarker}, false))
}

// Only the annotation is a marker. A label, or anything else, that happens to carry the name is
// configuration, and a later change to it blocks like any other.
func TestLaterChangeConflict_AMarkerIsAnAnnotationNotAName(t *testing.T) {
	for _, path := range []string{
		"spec.template.metadata.labels.rollme",
		"spec.template.metadata.annotations.my.rollme",
		"spec.template.spec.containers[0].env[0].rollme",
	} {
		change := recordedChange(0, fieldChange(path, "a", "b"))
		later := recordedChange(10, fieldChange(path, "b", "c"))
		assert.Contains(t, laterChangeConflict(change, targetsOf(change), []models.Event{later}, false), "changed again", path)
	}
}

func TestIsRolloutMarkerRecord(t *testing.T) {
	assert.True(t, isRolloutMarkerRecord(rollmePath, "a", "b"))
	assert.True(t, isRolloutMarkerRecord("spec.template.metadata.annotations.kubectl.kubernetes.io/restartedAt", nil, "t"))
	assert.True(t, isRolloutMarkerRecord("metadata.annotations.rollme", "a", "b"))
	assert.False(t, isRolloutMarkerRecord("spec.template.metadata.annotations.team", "x", "y"))
	assert.False(t, isRolloutMarkerRecord("spec.template.metadata.labels.rollme", "a", "b"))
	assert.False(t, isRolloutMarkerRecord(imagePath, "v1", "v2"))
	assert.False(t, isRolloutMarkerRecord("spec.template.metadata.annotations", map[string]any{"team": "x"}, map[string]any{"team": "y"}))
	// Nothing differs: not a marker change, and not a change at all.
	assert.False(t, isRolloutMarkerRecord("spec.template.metadata.annotations", map[string]any{"rollme": "a"}, map[string]any{"rollme": "a"}))
	// A value that is not a map cannot be read key by key, so it is not waved through.
	assert.False(t, isRolloutMarkerRecord("spec.template.metadata.annotations", "rollme", nil))
}

// An undo writes the change's new values back and expects to find its old ones — what the rollback
// left. Judged against the new side, as it was, the rollback's own record counted as "changed
// again", so an undo was refused as soon as the rollback it undoes had been recorded.
func TestLaterChangeConflict_UndoExpectsWhatTheRollbackLeft(t *testing.T) {
	change := recordedChange(0, fieldChange(imagePath, "v1", "v2"))
	rollback := recordedChange(10, fieldChange(imagePath, "v2", "v1"))

	assert.Empty(t, laterChangeConflict(change, targetsOf(change), []models.Event{rollback}, true))

	changedSince := recordedChange(20, fieldChange(imagePath, "v1", "v5"))
	assert.Contains(t, laterChangeConflict(change, targetsOf(change), []models.Event{rollback, changedSince}, true), "2026-03-04T10:20:00Z")

	// The same record still stops a second rollback of the change: it is already rolled back.
	assert.NotEmpty(t, laterChangeConflict(change, targetsOf(change), []models.Event{rollback}, false))
}

// Which field a refusal names used to follow Go's map order, so the same click could be refused
// for a different stated reason each time.
func TestLaterChangeConflict_NamesTheSameFieldEveryTime(t *testing.T) {
	change := recordedChange(0, fieldChange(imagePath, "v1", "v2"), fieldChange(envValue, "a", "b"), fieldChange("spec.replicas", 2, 3))
	later := []models.Event{recordedChange(10, fieldChange(imagePath, "v2", "v3"), fieldChange(envValue, "b", "c"), fieldChange("spec.replicas", 3, 4))}
	first := laterChangeConflict(change, targetsOf(change), later, false)
	require.NotEmpty(t, first)
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, laterChangeConflict(change, targetsOf(change), later, false))
	}
}
