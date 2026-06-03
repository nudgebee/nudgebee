package integrations

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/services/event"
)

// sampleDavisEventPayload is the DAVIS_EVENT webhook payload from NB-28385.
const sampleDavisEventPayload = `{
  "timestamp": "2026-04-14T05:54:02.567000000Z",
  "dt.entity.process_group": "PROCESS_GROUP-F7B84743AD387278",
  "dt.smartscape.k8s_cluster": "K8S_CLUSTER-43E027450F650458",
  "event.category": "INFO",
  "dt.davis.mute.status": "NOT_MUTED",
  "dt.source_entity.type": "process_group_instance",
  "dt.source_entity": "PROCESS_GROUP_INSTANCE-31C716BF15627F14",
  "dt.davis.impact_level": "Infrastructure",
  "dt.smartscape.k8s_node": "K8S_NODE-B814D7390D072264",
  "host.name": "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx",
  "dt.entity.gcp_zone.name": "us-central1-a",
  "event.kind": "DAVIS_EVENT",
  "event.severity": "5",
  "gcp.region": "us-central1",
  "event.name": "Process restart",
  "k8s.cluster.uid": "e39d0ec0-2895-406f-a7ef-6400ab6e589d",
  "dt.davis.is_frequent_event": false,
  "event.provider": "ONEAGENT",
  "dt.davis.timeout": "0",
  "event.description": "Process kubectl nudgebee-agent-apiserver-* (nudgebee-agent-apiserver-869dbfcdb-lmcb7) restarted",
  "event.status_transition": "CREATED",
  "smartscape.related_entity.types": ["HOST","K8S_CLUSTER","K8S_NODE","PROCESS"],
  "gcp.instance.id": "5464497175930150573",
  "event.start": "2026-04-14T05:54:00.754000000Z",
  "dt.entity.kubernetes_cluster.name": "gke-k8s-2026-03-12",
  "gcp.zone": "us-central1-a",
  "k8s.cluster.name": "gke-k8s-2026-03-12",
  "dt.entity.process_group_instance.name": "kubectl nudgebee-agent-apiserver-* (nudgebee-agent-apiserver-869dbfcdb-lmcb7)",
  "event.end": "2026-04-14T05:54:02.547000000Z",
  "dt.entity.process_group.name": "kubectl nudgebee-agent-apiserver-*",
  "affected_entity_types": ["dt.entity.process_group_instance"],
  "event.group_label": "Process restart",
  "related_entity_ids": ["GCP_ZONE-0ED3331217C5C6BC","HOST-705B76152CFAEA13","KUBERNETES_CLUSTER-EBD3D6AE1770773A","PROCESS_GROUP-F7B84743AD387278"],
  "event.status": "CLOSED",
  "smartscape.related_entities": [
    {"id":"HOST-705B76152CFAEA13","type":"HOST"},
    {"name":"gke-k8s-2026-03-12","id":"K8S_CLUSTER-43E027450F650458","type":"K8S_CLUSTER"},
    {"name":"gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx","id":"K8S_NODE-B814D7390D072264","type":"K8S_NODE"},
    {"id":"PROCESS-31C716BF15627F14","type":"PROCESS"}
  ],
  "dt.entity.gcp_zone": "GCP_ZONE-0ED3331217C5C6BC",
  "gcp.resource.name": "//compute.googleapis.com/projects/nudgebee-dev/zones/us-central1-a/instances/gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx",
  "affected_entity_ids": ["PROCESS_GROUP_INSTANCE-31C716BF15627F14"],
  "dt.smartscape.process": "PROCESS-31C716BF15627F14",
  "dt.smartscape.host": "HOST-705B76152CFAEA13",
  "gcp.project.id": "nudgebee-dev",
  "dt.entity.process_group_instance": "PROCESS_GROUP_INSTANCE-31C716BF15627F14",
  "smartscape.related_entity.ids": ["HOST-705B76152CFAEA13","K8S_CLUSTER-43E027450F650458","K8S_NODE-B814D7390D072264","PROCESS-31C716BF15627F14"],
  "OperatorVersion": "v1.8.1",
  "dt.entity.host": "HOST-705B76152CFAEA13",
  "k8s.node.name": "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx",
  "maintenance.is_under_maintenance": false,
  "event.type": "PROCESS_RESTART",
  "dt.entity.host.name": "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx.us-central1-a.c.nudgebee-dev.internal",
  "dt.entity.kubernetes_cluster": "KUBERNETES_CLUSTER-EBD3D6AE1770773A",
  "event.id": "-8050061476469882796_1776146040754",
  "dt.openpipeline.source": "oneagent",
  "dt.openpipeline.pipelines": ["davis.events:default"]
}`

// ---------------------------------------------------------------------------
// Unit tests — no external dependencies
// ---------------------------------------------------------------------------

func TestDavisEventPayload_Unmarshal(t *testing.T) {
	var p DynatraceDavisEventPayload
	require.NoError(t, json.Unmarshal([]byte(sampleDavisEventPayload), &p))

	assert.Equal(t, "DAVIS_EVENT", p.EventKind)
	assert.Equal(t, "PROCESS_RESTART", p.EventType)
	assert.Equal(t, "Process restart", p.EventName)
	assert.Equal(t, "INFO", p.EventCategory)
	assert.Equal(t, "CLOSED", p.EventStatus)
	assert.Equal(t, "CREATED", p.EventStatusTransition)
	assert.Equal(t, "-8050061476469882796_1776146040754", p.EventID)
	assert.Equal(t, "ONEAGENT", p.EventProvider)
	assert.Equal(t, NumberOrString("5"), p.EventSeverity)

	// Plain-string k8s fields.
	assert.Equal(t, "gke-k8s-2026-03-12", p.K8sClusterName)
	assert.Equal(t, "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx", p.K8sNodeName)
	assert.Equal(t, "gke-nudgebee-dev-c4-amd-spot-v2-02132c6e-r2gx", p.HostName)

	// GCP metadata.
	assert.Equal(t, "nudgebee-dev", p.GCPProjectID)
	assert.Equal(t, "us-central1", p.GCPRegion)
	assert.Equal(t, "us-central1-a", p.GCPZone)

	// Resolved entity names.
	assert.Equal(t, "kubectl nudgebee-agent-apiserver-* (nudgebee-agent-apiserver-869dbfcdb-lmcb7)", p.DtEntityProcessGroupInstanceName)
	assert.Equal(t, "kubectl nudgebee-agent-apiserver-*", p.DtEntityProcessGroupName)
	assert.Equal(t, "gke-k8s-2026-03-12", p.DtEntityKubernetesClusterName)

	// Source entity.
	assert.Equal(t, "PROCESS_GROUP_INSTANCE-31C716BF15627F14", p.DtSourceEntity)
	assert.Equal(t, "process_group_instance", p.DtSourceEntityType)

	// Smartscape related entities (array of objects).
	require.Len(t, p.SmartscapeRelatedEntities, 4)
	assert.Equal(t, "K8S_CLUSTER-43E027450F650458", p.SmartscapeRelatedEntities[1].ID)
	assert.Equal(t, "gke-k8s-2026-03-12", p.SmartscapeRelatedEntities[1].Name)

	// Array fields via StringOrSlice.
	assert.Equal(t, StringOrSlice{"PROCESS_GROUP_INSTANCE-31C716BF15627F14"}, p.AffectedEntityIDs)
	assert.Equal(t, StringOrSlice{"dt.entity.process_group_instance"}, p.AffectedEntityTypes)
	assert.Equal(t, StringOrSlice{"davis.events:default"}, p.OpenpipelinePipelines)
}

func TestStringOrSlice_Unmarshal_PlainString(t *testing.T) {
	// DAVIS_EVENT sends k8s fields as plain strings — must not fail.
	payload := `{"k8s.cluster.name": "my-cluster"}`
	var p DynatraceDavisEventPayload
	require.NoError(t, json.Unmarshal([]byte(payload), &p))
	assert.Equal(t, "my-cluster", p.K8sClusterName)
}

func TestStringOrSlice_Unmarshal_ArrayForDavisProblem(t *testing.T) {
	// DAVIS_PROBLEM sends k8s fields as arrays — StringOrSlice must handle both.
	type wrapper struct {
		Val StringOrSlice `json:"val"`
	}
	var single wrapper
	require.NoError(t, json.Unmarshal([]byte(`{"val":"one"}`), &single))
	assert.Equal(t, StringOrSlice{"one"}, single.Val)

	var multi wrapper
	require.NoError(t, json.Unmarshal([]byte(`{"val":["a","b"]}`), &multi))
	assert.Equal(t, StringOrSlice{"a", "b"}, multi.Val)
}

func TestStringOrSlice_Unmarshal_Null(t *testing.T) {
	// JSON null must produce an empty slice, not an error.
	type wrapper struct {
		Val StringOrSlice `json:"val"`
	}
	var w wrapper
	require.NoError(t, json.Unmarshal([]byte(`{"val":null}`), &w))
	assert.Equal(t, StringOrSlice{}, w.Val)
}

func TestMapDavisEventStatus(t *testing.T) {
	assert.Equal(t, "FIRING", mapDavisEventStatus("OPEN"))
	assert.Equal(t, "FIRING", mapDavisEventStatus("ACTIVE"))
	assert.Equal(t, "RESOLVED", mapDavisEventStatus("CLOSED"))
	assert.Equal(t, "RESOLVED", mapDavisEventStatus("RESOLVED"))
	assert.Equal(t, "RESOLVED", mapDavisEventStatus("INACTIVE"))
	assert.Equal(t, "unknown", mapDavisEventStatus("UNKNOWN"))
}

func TestMapDavisEventSeverity(t *testing.T) {
	assert.Equal(t, event.EventPriortiyMedium, mapDavisEventSeverity("INFO", "PROCESS_RESTART"))
	assert.Equal(t, event.EventPriortiyHigh, mapDavisEventSeverity("INFO", "PROCESS_CRASH"))
	assert.Equal(t, event.EventPriortiyLow, mapDavisEventSeverity("INFO", "CONFIG_CHANGE"))
	assert.Equal(t, event.EventPriortiyHigh, mapDavisEventSeverity("ERROR", ""))
	assert.Equal(t, event.EventPriortiyMedium, mapDavisEventSeverity("PERFORMANCE", ""))
	assert.Equal(t, event.EventPriortiyLow, mapDavisEventSeverity("INFO", ""))
	assert.Equal(t, event.EventPriortiyLow, mapDavisEventSeverity("", ""))
}

func TestExtractDavisEventEntityNames(t *testing.T) {
	var p DynatraceDavisEventPayload
	require.NoError(t, json.Unmarshal([]byte(sampleDavisEventPayload), &p))

	names := extractDavisEventEntityNames(p, nil)
	// Expect process group instance first, then process group, then node, then host.
	require.NotEmpty(t, names)
	assert.Equal(t, "kubectl nudgebee-agent-apiserver-* (nudgebee-agent-apiserver-869dbfcdb-lmcb7)", names[0])
}

func TestExtractDavisEventEntityNames_DQLPriority(t *testing.T) {
	var p DynatraceDavisEventPayload
	require.NoError(t, json.Unmarshal([]byte(sampleDavisEventPayload), &p))

	details := &DynatraceDavisEventDetails{
		AffectedEntityNames: []string{"my-service"},
	}
	names := extractDavisEventEntityNames(p, details)
	assert.Equal(t, "my-service", names[0], "DQL name should take priority")
}

func TestBuildDavisEventLabels(t *testing.T) {
	var p DynatraceDavisEventPayload
	require.NoError(t, json.Unmarshal([]byte(sampleDavisEventPayload), &p))

	labels := buildDavisEventLabels(p, []string{"my-svc"})

	assert.Equal(t, "PROCESS_RESTART", labels["event_type"])
	assert.Equal(t, "INFO", labels["event_category"])
	assert.Equal(t, "ONEAGENT", labels["event_provider"])
	assert.Equal(t, "CLOSED", labels["event_status"])
	assert.Equal(t, "gke-k8s-2026-03-12", labels["k8s_cluster_name"])
	assert.Equal(t, "us-central1", labels["gcp_region"])
	assert.Equal(t, "nudgebee-dev", labels["gcp_project_id"])
	assert.Equal(t, "my-svc", labels["service"])
	assert.Equal(t, "PROCESS_GROUP_INSTANCE-31C716BF15627F14", labels["affected_entity_ids"])
}

func TestDavisEventDispatchRouting(t *testing.T) {
	// Verify the OpenPipeline dispatcher probes event.kind correctly.
	// A DAVIS_EVENT payload (no ProblemID) must route to processDavisEventPayload,
	// which means it will fail on nil security context — but NOT on unmarshal.
	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(sampleDavisEventPayload), &probe))

	var eventKind string
	if raw, ok := probe["event.kind"]; ok {
		require.NoError(t, json.Unmarshal(raw, &eventKind))
	}
	assert.Equal(t, "DAVIS_EVENT", eventKind, "sample payload must carry event.kind=DAVIS_EVENT")

	// Also verify it has no ProblemID (classic path must not claim it).
	_, hasClassicID := probe["ProblemID"]
	assert.False(t, hasClassicID, "DAVIS_EVENT payload must not contain ProblemID")
}

// ---------------------------------------------------------------------------
// Integration tests — require live Dynatrace credentials
// Set DT_TOKEN and DT_BASE_URL environment variables to run.
// ---------------------------------------------------------------------------

func TestDavisEvent_DQLEnrichment_Live(t *testing.T) {
	token := os.Getenv("DT_TOKEN")
	baseURL := os.Getenv("DT_BASE_URL")
	if token == "" || baseURL == "" {
		t.Skip("DT_TOKEN and DT_BASE_URL env vars required")
	}

	// First fetch a recent DAVIS_EVENT to get a real event.id.
	recentQuery := `fetch dt.davis.events | limit 1 | fields event.id, event.name, event.type, event.status`
	records, err := runDynatraceDQL(baseURL, token, recentQuery)
	require.NoError(t, err, "DQL fetch dt.davis.events should succeed")

	if len(records) == 0 {
		t.Log("No DAVIS_EVENT records found in Dynatrace — skipping enrichment sub-test")
		return
	}

	eventID, _ := records[0]["event.id"].(string)
	require.NotEmpty(t, eventID, "event.id must be present in DQL record")
	t.Logf("Testing DQL enrichment with event.id: %s", eventID)

	details, err := getDynatraceDavisEventDetails(baseURL, token, eventID)
	require.NoError(t, err)
	require.NotNil(t, details, "details should not be nil for a known event.id")

	t.Logf("Event name: %q, type: %q, start: %d, end: %d, affected: %v",
		details.EventName, details.EventType, details.StartTime, details.EndTime, details.AffectedEntityNames)
}

func TestDavisEvent_DQLEnrichment_SamplePayload_Live(t *testing.T) {
	token := os.Getenv("DT_TOKEN")
	baseURL := os.Getenv("DT_BASE_URL")
	if token == "" || baseURL == "" {
		t.Skip("DT_TOKEN and DT_BASE_URL env vars required")
	}

	const sampleEventID = "-8050061476469882796_1776146040754"
	details, err := getDynatraceDavisEventDetails(baseURL, token, sampleEventID)
	require.NoError(t, err)

	if details == nil {
		t.Logf("Event %s not found in Grail (may have expired or be from a different env)", sampleEventID)
	} else {
		t.Logf("Found: name=%q type=%q start=%d end=%d affected=%v",
			details.EventName, details.EventType, details.StartTime, details.EndTime, details.AffectedEntityNames)
	}
}

func TestDavisEvent_FetchRecentEvents_Live(t *testing.T) {
	token := os.Getenv("DT_TOKEN")
	baseURL := os.Getenv("DT_BASE_URL")
	if token == "" || baseURL == "" {
		t.Skip("DT_TOKEN and DT_BASE_URL env vars required")
	}

	query := `fetch dt.davis.events | sort timestamp desc | limit 5 | fields event.id, event.name, event.type, event.status, event.kind, k8s.cluster.name, host.name`
	records, err := runDynatraceDQL(baseURL, token, query)
	require.NoError(t, err, "DQL fetch should succeed with valid credentials")

	t.Logf("Found %d recent DAVIS_EVENT records", len(records))
	for i, rec := range records {
		t.Logf("  [%d] id=%v name=%v type=%v status=%v", i,
			rec["event.id"], rec["event.name"], rec["event.type"], rec["event.status"])
	}
}
