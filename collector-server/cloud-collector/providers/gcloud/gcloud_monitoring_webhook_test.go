package gcloud

import (
	"encoding/json"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each case is one GCP incident in both shapes it reaches us in: the webhook
// notification-channel payload and the v3 alerts.list entry polling reads. The
// payload shapes are copied from real deliveries (names replaced).
var webhookParityCases = []struct {
	name        string
	webhook     string
	alert       string
	wantSubject string
	wantEventId string
}{
	{
		// #34377: the webhook used to name the instance "sample-db" (its display name),
		// and the traces enricher found no spans for it.
		name: "cloudsql log-based metric",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.cloudsql1","scoping_project_id":"example-project",
			"url":"https://console.cloud.google.com/monitoring/alerting/alerts/0.cloudsql1?project=example-project",
			"state":"open","started_at":1790670860,"ended_at":0,
			"summary":"logging/user/slow-queries for example-project sample-db is above the threshold of 10.000 with a value of 12.000.",
			"observed_value":"12.000","threshold_value":"10",
			"resource":{"type":"cloudsql_database","labels":{"region":"us-central","project_id":"example-project","database_id":"example-project:sample-db"}},
			"resource_display_name":"sample-db","resource_type_display_name":"Cloud SQL Database",
			"metric":{"type":"logging.googleapis.com/user/slow-queries","displayName":"logging/user/slow-queries","labels":{"log":"cloudsql.googleapis.com/postgres.log"}},
			"policy_name":"slow-queries","severity":"Warning","policy_user_labels":null,
			"condition_name":"Slow queries > 5 in 10 min",
			"condition":{"name":"projects/example-project/alertPolicies/1001/conditions/2001","displayName":"Slow queries > 5 in 10 min"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.cloudsql1","state":"OPEN","openTime":"2026-09-29T08:34:20Z",
			"resource":{"type":"cloudsql_database","labels":{"database_id":"example-project:sample-db","project_id":"example-project","region":"us-central"}},
			"metric":{"type":"logging.googleapis.com/user/slow-queries","labels":{"log":"cloudsql.googleapis.com/postgres.log"}},
			"policy":{"name":"projects/example-project/alertPolicies/1001","displayName":"slow-queries","severity":"WARNING"}}`,
		wantSubject: "example-project:sample-db",
		wantEventId: "projects/example-project/alertPolicies/1001:cloudsql_database:example-project:sample-db",
	},
	{
		name: "cloudsql closed",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.cloudsql1","scoping_project_id":"example-project",
			"state":"closed","started_at":1790670860,"ended_at":1790671460,
			"resource":{"type":"cloudsql_database","labels":{"region":"us-central","project_id":"example-project","database_id":"example-project:sample-db"}},
			"resource_display_name":"sample-db",
			"metric":{"type":"cloudsql.googleapis.com/database/cpu/utilization","labels":{}},
			"policy_name":"db-cpu","severity":"Critical",
			"condition":{"name":"projects/example-project/alertPolicies/1002/conditions/2002"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.cloudsql1","state":"CLOSED",
			"openTime":"2026-09-29T08:34:20Z","closeTime":"2026-09-29T08:44:20Z",
			"resource":{"type":"cloudsql_database","labels":{"database_id":"example-project:sample-db","project_id":"example-project","region":"us-central"}},
			"metric":{"type":"cloudsql.googleapis.com/database/cpu/utilization","labels":{}},
			"policy":{"name":"projects/example-project/alertPolicies/1002","displayName":"db-cpu","severity":"CRITICAL"}}`,
		wantSubject: "example-project:sample-db",
		wantEventId: "projects/example-project/alertPolicies/1002:cloudsql_database:example-project:sample-db",
	},
	{
		// The webhook used to give GKE log-match incidents no subject at all, so every
		// nodepool under one policy shared a single fingerprint.
		name: "gke nodepool log match",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.nodepool1","scoping_project_id":"example-project",
			"state":"open","started_at":1788656189,"ended_at":0,
			"resource":{"type":"gke_nodepool","labels":{"location":"us-central1","project_id":"example-project","cluster_name":"sample-cluster","nodepool_name":"sample-pool"}},
			"resource_display_name":"","resource_type_display_name":"GKE Node Pool Operations",
			"metric":{"type":"","labels":{},"displayName":""},
			"policy_name":"GKE Resource Upgrade Started","severity":"No severity",
			"condition_name":"Log match condition",
			"condition":{"name":"projects/example-project/alertPolicies/1003/conditions/2003","displayName":"Log match condition"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.nodepool1","state":"OPEN","openTime":"2026-09-06T00:56:29Z",
			"resource":{"type":"gke_nodepool","labels":{"cluster_name":"sample-cluster","location":"us-central1","nodepool_name":"sample-pool","project_id":"example-project"}},
			"log":{},
			"policy":{"name":"projects/example-project/alertPolicies/1003","displayName":"GKE Resource Upgrade Started","severity":""}}`,
		wantSubject: "sample-pool",
		wantEventId: "projects/example-project/alertPolicies/1003:gke_nodepool:sample-pool",
	},
	{
		name: "gke cluster log match",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.cluster1","scoping_project_id":"example-project",
			"state":"open","started_at":1788656189,
			"resource":{"type":"gke_cluster","labels":{"location":"us-central1","project_id":"example-project","cluster_name":"sample-cluster"}},
			"metric":{"type":"","labels":{}},
			"policy_name":"GKE Resource Upgrade Started","severity":"No severity",
			"condition":{"name":"projects/example-project/alertPolicies/1003/conditions/2003"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.cluster1","state":"OPEN","openTime":"2026-09-06T00:56:29Z",
			"resource":{"type":"gke_cluster","labels":{"cluster_name":"sample-cluster","location":"us-central1","project_id":"example-project"}},
			"log":{},
			"policy":{"name":"projects/example-project/alertPolicies/1003","displayName":"GKE Resource Upgrade Started"}}`,
		wantSubject: "sample-cluster",
		wantEventId: "projects/example-project/alertPolicies/1003:gke_cluster:sample-cluster",
	},
	{
		name: "k8s cluster log match",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.k8scluster1","scoping_project_id":"example-project",
			"state":"open","started_at":1788656189,
			"resource":{"type":"k8s_cluster","labels":{"location":"us-central1","project_id":"example-project","cluster_name":"sample-cluster"}},
			"metric":{"type":"","labels":{}},
			"policy_name":"Kubernetes Secret Modified","severity":"Warning",
			"condition":{"name":"projects/example-project/alertPolicies/1006/conditions/2006"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.k8scluster1","state":"OPEN","openTime":"2026-09-06T00:56:29Z",
			"resource":{"type":"k8s_cluster","labels":{"cluster_name":"sample-cluster","location":"us-central1","project_id":"example-project"}},
			"log":{},
			"policy":{"name":"projects/example-project/alertPolicies/1006","displayName":"Kubernetes Secret Modified","severity":"WARNING"}}`,
		wantSubject: "sample-cluster",
		wantEventId: "projects/example-project/alertPolicies/1006:k8s_cluster:sample-cluster",
	},
	{
		name: "cloud run metric threshold",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.run1","scoping_project_id":"example-project",
			"state":"open","started_at":1789465227,
			"summary":"Instance Count for sample-service is above the threshold of 0.800 with a value of 1.000.",
			"observed_value":"1.000","threshold_value":"0.8",
			"resource":{"type":"cloud_run_revision","labels":{"location":"us-central1","project_id":"example-project","service_name":"sample-service","revision_name":"sample-service-00009-abc","configuration_name":"sample-service"}},
			"resource_display_name":"",
			"metric":{"type":"run.googleapis.com/container/instance_count","labels":{"state":"idle"},"displayName":"Instance Count"},
			"policy_name":"sample-service-instance-count","severity":"No severity",
			"condition":{"name":"projects/example-project/alertPolicies/1004/conditions/2004"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.run1","state":"OPEN","openTime":"2026-09-15T09:40:27Z",
			"resource":{"type":"cloud_run_revision","labels":{"location":"us-central1","project_id":"example-project","service_name":"sample-service","revision_name":"sample-service-00009-abc","configuration_name":"sample-service"}},
			"metric":{"type":"run.googleapis.com/container/instance_count","labels":{"state":"idle"}},
			"policy":{"name":"projects/example-project/alertPolicies/1004","displayName":"sample-service-instance-count"}}`,
		wantSubject: "sample-service",
		wantEventId: "projects/example-project/alertPolicies/1004:cloud_run_revision:sample-service",
	},
	{
		name: "gce instance named by metric label",
		webhook: `{"version":"1.2","incident":{
			"incident_id":"0.vm1","scoping_project_id":"example-project",
			"state":"open","started_at":1790670860,
			"resource":{"type":"gce_instance","labels":{"project_id":"example-project","zone":"us-central1-a","instance_id":"1234567890"}},
			"resource_display_name":"sample-vm",
			"metric":{"type":"compute.googleapis.com/instance/cpu/utilization","labels":{"instance_name":"sample-vm"}},
			"policy_name":"vm-cpu","severity":"Error",
			"condition":{"name":"projects/example-project/alertPolicies/1005/conditions/2005"}}}`,
		alert: `{"name":"projects/example-project/alerts/0.vm1","state":"OPEN","openTime":"2026-09-29T08:34:20Z",
			"resource":{"type":"gce_instance","labels":{"project_id":"example-project","zone":"us-central1-a","instance_id":"1234567890"}},
			"metric":{"type":"compute.googleapis.com/instance/cpu/utilization","labels":{"instance_name":"sample-vm"}},
			"policy":{"name":"projects/example-project/alertPolicies/1005","displayName":"vm-cpu","severity":"ERROR"}}`,
		wantSubject: "sample-vm",
		wantEventId: "projects/example-project/alertPolicies/1005:gce_instance:sample-vm",
	},
}

// webhookOnlyLabels are carried from the webhook delivery and have no v3 Alert
// counterpart; they are the only labels allowed to differ.
var webhookOnlyLabels = []string{"gcp_console_url", "observed_value", "threshold_value", "condition_name"}

func TestConvertWebhookIncidentMatchesPolling(t *testing.T) {
	account := providers.Account{ID: "acc-1", AccountNumber: "example-project", AccountName: "example", CloudProvider: "GCP"}

	for _, tc := range webhookParityCases {
		t.Run(tc.name, func(t *testing.T) {
			var alert Alert
			require.NoError(t, json.Unmarshal([]byte(tc.alert), &alert))
			polled := convertAlertToEvent(nil, nil, &alert, &account)

			pushed, err := ConvertWebhookIncident(nil, account, []byte(tc.webhook))
			require.NoError(t, err)

			assert.Equal(t, tc.wantSubject, polled.ResourceId, "polling subject")
			assert.Equal(t, tc.wantEventId, polled.EventId, "polling fingerprint")

			assert.Equal(t, polled.ResourceId, pushed.ResourceId, "subject")
			assert.Equal(t, polled.EventId, pushed.EventId, "fingerprint")
			assert.Equal(t, polled.FindingId, pushed.FindingId, "finding_id")
			assert.Equal(t, polled.ResourceType, pushed.ResourceType, "subject type")
			assert.Equal(t, polled.ResourceServiceName, pushed.ResourceServiceName, "subject namespace")
			assert.Equal(t, polled.ResourceRegion, pushed.ResourceRegion, "region")
			assert.Equal(t, polled.EventSeverity, pushed.EventSeverity, "severity")
			assert.Equal(t, polled.EventStatus, pushed.EventStatus, "status")
			assert.Equal(t, polled.EventName, pushed.EventName, "aggregation key")
			assert.Equal(t, polled.Title, pushed.Title, "title")
			assert.True(t, polled.Date.Equal(pushed.Date), "date: polled %s, pushed %s", polled.Date, pushed.Date)

			pushedLabels := map[string]string{}
			for k, v := range pushed.Labels {
				pushedLabels[k] = v
			}
			for _, k := range webhookOnlyLabels {
				delete(pushedLabels, k)
			}
			assert.Equal(t, polled.Labels, pushedLabels, "labels")
		})
	}
}

func TestConvertWebhookIncidentKeepsDeliveryContext(t *testing.T) {
	account := providers.Account{AccountNumber: "example-project", CloudProvider: "GCP"}
	event, err := ConvertWebhookIncident(nil, account, []byte(webhookParityCases[0].webhook))
	require.NoError(t, err)

	assert.Contains(t, event.Description, "is above the threshold of 10.000 with a value of 12.000")
	assert.Equal(t, "12.000", event.Labels["observed_value"])
	assert.Equal(t, "10", event.Labels["threshold_value"])
	assert.Equal(t, "Slow queries > 5 in 10 min", event.Labels["condition_name"])
	assert.Contains(t, event.Labels["gcp_console_url"], "0.cloudsql1")
}

func TestConvertWebhookIncidentFindingIdUsesScopingProject(t *testing.T) {
	// Under a multi-project metrics scope the resource lives in another project; the
	// incident is still named under the scoping project, as alerts.list names it.
	body := `{"incident":{"incident_id":"0.x","scoping_project_id":"scope-project",
		"state":"open","started_at":1790670860,
		"resource":{"type":"cloudsql_database","labels":{"project_id":"member-project","database_id":"member-project:sample-db"}},
		"metric":{"type":"cloudsql.googleapis.com/database/cpu/utilization"},
		"policy_name":"db-cpu","condition":{"name":"projects/scope-project/alertPolicies/1/conditions/2"}}}`
	event, err := ConvertWebhookIncident(nil, providers.Account{AccountNumber: "scope-project"}, []byte(body))
	require.NoError(t, err)
	assert.Equal(t, "projects/scope-project/alerts/0.x", event.FindingId)
}

func TestConvertWebhookIncidentRejectsInvalidPayload(t *testing.T) {
	account := providers.Account{AccountNumber: "example-project"}

	_, err := ConvertWebhookIncident(nil, account, []byte(`not json`))
	assert.Error(t, err)

	_, err = ConvertWebhookIncident(nil, account, []byte(`{"incident":{"incident_id":"  "}}`))
	assert.Error(t, err)
}
