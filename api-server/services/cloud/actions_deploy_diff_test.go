package cloud

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestQueryDeploymentDiffResponseParse locks the client-side parse of the collector's
// wrapped response shape ({data:{revisions,status}}, rev keys name/create_time/creator/
// spec_yaml) — captured verbatim from a live /v1/cloud/query_deployment_diff call. A
// json-tag drift here would silently yield zero revisions (no diff card).
func TestQueryDeploymentDiffResponseParse(t *testing.T) {
	body := []byte(`{"data":{"revisions":[
		{"name":"frontoffice-v260629-0611-cba-001-274c4d62","create_time":1782714027433,"creator":"github-actions-wif@live-fullspectrum.iam.gserviceaccount.com","spec_yaml":"containers:\n    - name: frontoffice-1\n      image: gcr.io/live-fullspectrum/frontoffice@sha256:d9e0\n"},
		{"name":"frontoffice-v260626-1144-set-24687-52f6cafd","create_time":1782474787489,"creator":"github-actions-wif@live-fullspectrum.iam.gserviceaccount.com","spec_yaml":"containers:\n    - name: frontoffice-1\n      image: gcr.io/live-fullspectrum/frontoffice@sha256:cdad\n"}
	],"status":"Complete"}}`)

	out := queryDeploymentDiffResponse{}
	assert.NoError(t, json.Unmarshal(body, &out))
	assert.Equal(t, "Complete", out.Data.Status)
	assert.Len(t, out.Data.Revisions, 2)

	newest, prev := out.Data.Revisions[0], out.Data.Revisions[1]
	assert.Equal(t, "frontoffice-v260629-0611-cba-001-274c4d62", newest.Name)
	assert.Equal(t, int64(1782714027433), newest.CreateTime)
	assert.Contains(t, newest.SpecYAML, "sha256:d9e0")
	assert.Contains(t, prev.SpecYAML, "sha256:cdad")

	// The evidence the enricher builds from this: data.{old,new} = prev/newest spec.
	assert.NotEqual(t, newest.SpecYAML, prev.SpecYAML)
	assert.Greater(t, newest.CreateTime, prev.CreateTime) // newest-first ordering
}

func TestGcpCloudRunServiceName(t *testing.T) {
	// resource_service_name is canonical and preferred.
	assert.Equal(t, "frontoffice", gcpCloudRunServiceName(map[string]string{
		"resource_service_name": "frontoffice",
		"gcp_event_instance":    "frontoffice",
	}))
	// Falls back to gcp_event_instance when resource_service_name is absent.
	assert.Equal(t, "frontoffice", gcpCloudRunServiceName(map[string]string{
		"gcp_event_instance": "frontoffice",
	}))
	// Must NOT use gcp_event_instance when it is just the incident-id fallback.
	assert.Equal(t, "", gcpCloudRunServiceName(map[string]string{
		"gcp_event_instance": "12345",
		"gcp_incident_id":    "12345",
	}))
	// gcp_service_name (the GCP product, "Cloud Run") must never be used as the service.
	assert.Equal(t, "", gcpCloudRunServiceName(map[string]string{
		"gcp_service_name": "Cloud Run",
	}))
}

func TestGcpCloudRunRegion(t *testing.T) {
	assert.Equal(t, "us-central1", gcpCloudRunRegion(map[string]string{"gcp_region": "us-central1"}))
	assert.Equal(t, "asia-south1", gcpCloudRunRegion(map[string]string{"resource_location": "asia-south1"}))
	assert.Equal(t, "us-central1", gcpCloudRunRegion(map[string]string{
		"gcp_region":        "us-central1",
		"resource_location": "asia-south1",
	}))
	assert.Equal(t, "", gcpCloudRunRegion(map[string]string{}))
}

func TestFormatDeployDelta(t *testing.T) {
	assert.Equal(t, "30s", formatDeployDelta(30*time.Second))
	assert.Equal(t, "5m", formatDeployDelta(5*time.Minute))
	assert.Equal(t, "2h 15m", formatDeployDelta(2*time.Hour+15*time.Minute))
	assert.Equal(t, "1d 2h", formatDeployDelta(26*time.Hour))
}

// TestCloudDeploymentDiffResponseShape locks the contract the frontend LastDeploymentCard
// relies on: evidence top-level type == "diff" (the render gate) and data == {old,new}.
func TestCloudDeploymentDiffResponseShape(t *testing.T) {
	r := cloudDeploymentDiffResponse{
		Data:           map[string]string{"old": "image: app:v1\n", "new": "image: app:v2\n"},
		AdditionalInfo: map[string]any{"title": "Last Deployment Change", "action_name": "cloud_deployment_diff"},
	}
	assert.Equal(t, "diff", r.GetFormatName())
	data, ok := r.GetData().(map[string]string)
	assert.True(t, ok)
	assert.Equal(t, "image: app:v1\n", data["old"])
	assert.Equal(t, "image: app:v2\n", data["new"])
	assert.Equal(t, "cloud_deployment_diff", r.GetAdditionalInfo()["action_name"])
}
