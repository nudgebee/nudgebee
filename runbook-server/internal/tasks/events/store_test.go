package events

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/runbook/config"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/services/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #39585: a recovery may close every open firing of an alert by its
// fingerprint, so finding_id is optional only on a resolve that carries one.
func TestValidateFindingId(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event service.Event
		ok    bool
	}{
		{"firing with finding_id", service.Event{Status: service.EventStatusFiring, FindingId: "f1"}, true},
		{"firing without finding_id", service.Event{Status: service.EventStatusFiring, Fingerprint: "fp"}, false},
		{"resolved by fingerprint", service.Event{Status: service.EventStatusResolved, Fingerprint: "fp"}, true},
		{"closed by fingerprint", service.Event{Status: service.EventStatusClosed, Fingerprint: "fp"}, true},
		{"lowercase resolved by fingerprint", service.Event{Status: "resolved", Fingerprint: "fp"}, true},
		{"resolved with neither id", service.Event{Status: service.EventStatusResolved}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFindingId(tc.event)
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// The saved-workflow schema must accept a resolve without finding_id and still
// reject a firing without one.
func TestInputSchemaFindingIdRequiredOnlyWhenFiring(t *testing.T) {
	event := func(status string) map[string]any {
		return map[string]any{
			"title":           "calico-node Unavailable Replicas exceeds 0",
			"description":     "Site24x7 recovery",
			"aggregation_key": "site24x7",
			"finding_type":    "issue",
			"subject_name":    "calico-node",
			"priority":        "HIGH",
			"fingerprint":     "calico-node:unavailable",
			"status":          status,
		}
	}
	schema := (&EventsStoreTask{}).InputSchema()

	assert.NoError(t, schema.Validate(map[string]any{"event": event("RESOLVED")}))
	assert.ErrorContains(t, schema.Validate(map[string]any{"event": event("FIRING")}), "finding_id")
}

func TestSplitEventEvidences(t *testing.T) {
	legacyItem := map[string]any{"type": "markdown", "data": "body", "filename": "x.md"}
	halfFilled := map[string]any{"type": "markdown"}
	markdown := map[string]any{"type": "markdown", "title": "Rollout", "content": "pods not ready"}
	table := map[string]any{"type": "table", "headers": []any{"Pod"}, "rows": []any{[]any{"api-7f9"}}}

	legacy, authored, err := splitEventEvidences([]any{legacyItem, "raw", halfFilled, markdown, table})
	require.NoError(t, err)
	assert.Equal(t, []any{legacyItem, "raw", halfFilled}, legacy)
	require.Len(t, authored, 2)
	assert.Equal(t, "pods not ready", authored[0].Content)
	assert.Equal(t, []string{"Pod"}, authored[1].Headers)
	assert.Equal(t, `[["api-7f9"]]`, authored[1].Rows)

	legacy, authored, err = splitEventEvidences(nil)
	require.NoError(t, err)
	assert.Empty(t, legacy)
	assert.Empty(t, authored)
}

func TestSplitEventEvidencesNamesTheBadCard(t *testing.T) {
	for _, tc := range []struct {
		name string
		item map[string]any
		want string
	}{
		{"markdown without content", map[string]any{"type": "markdown", "title": "t"}, "content is required"},
		{"unknown type", map[string]any{"type": "chart", "title": "t"}, "type must be one of"},
		{"json not an object", map[string]any{"type": "json", "json_data": "3"}, "JSON object or array"},
		{"empty json", map[string]any{"type": "json", "json_data": "{}"}, "must not be empty"},
		{"table without headers", map[string]any{"type": "table", "rows": `[["a"]]`}, "headers are required"},
		{"table row not an array", map[string]any{"type": "table", "headers": []any{"A"}, "rows": `["a"]`}, "row 1"},
		{"bad severity", map[string]any{"type": "markdown", "content": "c", "severity": "Low"}, "severity"},
		{"json with only a severity", map[string]any{"type": "json", "severity": "High"}, "json_data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := splitEventEvidences([]any{map[string]any{"type": "markdown", "content": "ok"}, tc.item})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "evidence 2")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestEvidenceTaskIDIsDistinctPerCard(t *testing.T) {
	assert.Equal(t, "store_1#evidence-1", evidenceTaskID("store_1", 0))
	assert.NotEqual(t, evidenceTaskID("store_1", 0), evidenceTaskID("store_1", 1))
}

func TestInputSchemaEvidenceItemsMirrorAddEvidence(t *testing.T) {
	store := (&EventsStoreTask{}).InputSchema()
	items := store.Properties["event"].Schema.Properties["evidences"].Schema.Properties
	addEvidence := (&EventsAddEvidenceTask{}).InputSchema().Properties

	for field, prop := range items {
		other, ok := addEvidence[field]
		require.Truef(t, ok, "%s is not an add_evidence field", field)
		assert.Equal(t, other.Options, prop.Options, field)
		assert.Equal(t, other.VisibleWhen, prop.VisibleWhen, field)
		assert.Equal(t, other.RequiredWhen, prop.RequiredWhen, field)
	}
	assert.True(t, items["type"].Required)
	assert.NotContains(t, items, "data")
	assert.NotContains(t, items, "filename")
}

func TestInputSchemaAcceptsLegacyAndAuthoredEvidences(t *testing.T) {
	schema := (&EventsStoreTask{}).InputSchema()
	event := map[string]any{
		"title": "t", "description": "d", "aggregation_key": "a", "finding_id": "f", "finding_type": "issue",
		"subject_name": "s", "priority": "HIGH", "status": "FIRING",
	}
	event["evidences"] = []any{
		map[string]any{"type": "markdown", "data": "body", "filename": "x.md"},
		map[string]any{"type": "table", "headers": []any{"Pod"}, "rows": `[["api"]]`},
	}
	assert.NoError(t, schema.Validate(map[string]any{"event": event}))
}

func TestInputSchemaEventFieldsHaveDistinctOrders(t *testing.T) {
	seen := map[int]string{}
	for name, prop := range (&EventsStoreTask{}).InputSchema().Properties["event"].Schema.Properties {
		require.NotZerof(t, prop.Order, "%s has no order", name)
		other, dup := seen[prop.Order]
		require.Falsef(t, dup, "%s and %s share order %d", name, other, prop.Order)
		seen[prop.Order] = name
	}
}

// eventsServer stands in for api-server's /rpc/event and records each action.
func eventsServer(t *testing.T, failAttach bool) *[]map[string]any {
	t.Helper()
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, body)
		action, _ := body["action"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")
		switch action["name"] {
		case "trigger_investigation":
			_, _ = w.Write([]byte(`{"id":["event-1"]}`))
		case "add_event_evidence":
			if failAttach {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
				return
			}
			_, _ = w.Write([]byte(`{"event_id":"event-1","added":1}`))
		}
	}))
	t.Cleanup(server.Close)
	previous := config.Config.ServiceEndpoint
	config.Config.ServiceEndpoint = server.URL
	t.Cleanup(func() { config.Config.ServiceEndpoint = previous })
	return &requests
}

func storeParams(evidences []any) map[string]any {
	return map[string]any{"event": map[string]any{
		"title": "t", "description": "d", "aggregation_key": "a", "finding_id": "f", "finding_type": "issue",
		"subject_name": "s", "priority": "HIGH", "status": "FIRING", "cluster": "c", "account_id": "acc",
		"evidences": evidences,
	}}
}

func TestExecuteAttachesAuthoredEvidencesAfterStoring(t *testing.T) {
	requests := eventsServer(t, false)
	legacyItem := map[string]any{"type": "markdown", "data": "body", "filename": "x.md"}
	taskCtx := testutils.NewTestTaskContext("tenant", "acc", "user", slog.Default())

	id, err := (&EventsStoreTask{}).Execute(taskCtx, storeParams([]any{
		legacyItem,
		map[string]any{"type": "markdown", "content": "one"},
		map[string]any{"type": "json", "json_data": `{"a":1}`},
	}))
	require.NoError(t, err)
	assert.Equal(t, "event-1", id)

	require.Len(t, *requests, 3)
	stored := (*requests)[0]["input"].(map[string]any)["events"].([]any)[0].(map[string]any)
	assert.Len(t, stored["evidences"], 1, "only the pre-built evidence travels with the event")

	taskIDs := map[string]bool{}
	for _, req := range (*requests)[1:] {
		assert.Equal(t, "add_event_evidence", req["action"].(map[string]any)["name"])
		input := req["input"].(map[string]any)
		assert.Equal(t, "event-1", input["event_id"])
		taskIDs[input["source_workflow"].(map[string]any)["task_id"].(string)] = true
	}
	assert.Len(t, taskIDs, 2)
}

func TestExecuteRejectsABadCardBeforeStoring(t *testing.T) {
	requests := eventsServer(t, false)
	taskCtx := testutils.NewTestTaskContext("tenant", "acc", "user", slog.Default())

	_, err := (&EventsStoreTask{}).Execute(taskCtx, storeParams([]any{map[string]any{"type": "markdown", "title": "no body"}}))
	require.ErrorContains(t, err, "content is required")
	assert.Empty(t, *requests)
}

func TestExecuteFailsNamingTheStoredEventWhenAttachFails(t *testing.T) {
	eventsServer(t, true)
	taskCtx := testutils.NewTestTaskContext("tenant", "acc", "user", slog.Default())

	_, err := (&EventsStoreTask{}).Execute(taskCtx, storeParams([]any{map[string]any{"type": "markdown", "content": "one"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event event-1 was stored")
}
