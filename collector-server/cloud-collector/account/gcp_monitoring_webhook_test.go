package account

import (
	"testing"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/gcloud"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The row returned for a webhook delivery must be the row polling stores for the same
// incident: the traces enricher scopes Cloud SQL spans by the project:instance subject,
// and dedup/resolve key on fingerprint and finding_id (#34377).
func TestGCPWebhookEventFromRowMatchesPollingRow(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin("tenant-1")
	account := providers.Account{ID: "acc-1", AccountNumber: "example-project", AccountName: "example", CloudProvider: "GCP"}
	body := `{"version":"1.2","incident":{
		"incident_id":"0.cloudsql1","scoping_project_id":"example-project",
		"state":"open","started_at":1790670860,
		"summary":"logging/user/slow-queries for example-project sample-db is above the threshold.",
		"resource":{"type":"cloudsql_database","labels":{"region":"us-central","project_id":"example-project","database_id":"example-project:sample-db"}},
		"resource_display_name":"sample-db",
		"metric":{"type":"logging.googleapis.com/user/slow-queries","labels":{"log":"cloudsql.googleapis.com/postgres.log"}},
		"policy_name":"slow-queries","severity":"Warning",
		"condition":{"name":"projects/example-project/alertPolicies/1001/conditions/2001"}}}`

	event, err := gcloud.ConvertWebhookIncident(nil, account, []byte(body))
	require.NoError(t, err)

	row, err := gcpWebhookEventRow(ctx, event, account, account.ID)
	require.NoError(t, err)
	got, err := gcpWebhookEventFromRow(row)
	require.NoError(t, err)

	assert.Equal(t, "example-project:sample-db", got.SubjectName)
	assert.Equal(t, "cloudsql_database", got.SubjectType)
	assert.Equal(t, "Cloud SQL", got.SubjectNamespace)
	assert.Equal(t, "arn:gcp:cloud sql:us-central:example-project:cloudsql_database:example-project:sample-db", got.ServiceKey)
	assert.Equal(t, "projects/example-project/alertPolicies/1001:cloudsql_database:example-project:sample-db", got.Fingerprint)
	assert.Equal(t, "projects/example-project/alerts/0.cloudsql1", got.FindingId)
	assert.Equal(t, "slow-queries", got.AggregationKey)
	assert.Equal(t, "cloudsql_database: slow-queries", got.Title)
	assert.Contains(t, got.Description, "is above the threshold")
	assert.Equal(t, "MEDIUM", got.Priority)
	assert.Equal(t, "FIRING", got.Status)
	assert.Empty(t, got.CloudResourceId, "unlinked until linkCloudResourceIds finds the resource")

	resourceId := "res-1"
	row["cloud_resource_id"] = &resourceId
	got, err = gcpWebhookEventFromRow(row)
	require.NoError(t, err)
	assert.Equal(t, "res-1", got.CloudResourceId)
	assert.Equal(t, "example-project:sample-db", got.Labels["gcp_event_instance"])
	// cloud_logs narrows a log-based metric alert to the log it counts only with all
	// three of these (#36870).
	assert.Equal(t, "cloudsql.googleapis.com/postgres.log", got.Labels["metric_log"])
	assert.Equal(t, "example-project", got.Labels["gcp_project_id"])
	assert.Equal(t, "projects/example-project/alertPolicies/1001", got.Labels["gcp_policy_id"])
}
