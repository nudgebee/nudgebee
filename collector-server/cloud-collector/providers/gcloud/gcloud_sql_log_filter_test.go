package gcloud

import (
	"context"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"
)

// A Cloud SQL monitoring alert carries gcp_event_instance as the FULL database_id
// ("project:instance"), while the resource inventory passes a bare instance name.
// GetLogFilter prepended the project unconditionally, producing
// database_id="example-project:example-project:sample-db" — a filter that matches
// zero log entries, verified against live Cloud Logging. That is why every plain
// Cloud SQL metric alert (CPU, memory, disk) rendered with no log card at all,
// while log-based-metric alerts were fine: they set log_group_name, which skips
// this code path.
//
// Only the already-qualified branch is exercised here; the bare-name branch needs
// a real GCP session for the project id.
func TestCloudSQLGetLogFilter_AlreadyQualifiedDatabaseID(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	svc := &cloudSQLService{}

	tests := []struct {
		name       string
		resourceID string
		want       string
	}{
		{
			// Shaped exactly like the value on event 4ede665d (a plain CPU alert).
			name:       "monitoring alert instance is already project-qualified",
			resourceID: "example-project:sample-db",
			want:       `resource.type="cloudsql_database" resource.labels.database_id="example-project:sample-db"`,
		},
		{
			name:       "instance name containing hyphens stays intact",
			resourceID: "example-project:sample-db-replica",
			want:       `resource.type="cloudsql_database" resource.labels.database_id="example-project:sample-db-replica"`,
		},
		{
			name:       "empty resource id keeps the broad filter",
			resourceID: "",
			want:       `resource.type="cloudsql_database"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.GetLogFilter(ctx, providers.Account{}, tt.resourceID)
			if got != tt.want {
				t.Errorf("filter:\n got=%s\nwant=%s", got, tt.want)
			}
		})
	}
}

// Guard the specific malformation: the project id must never appear twice.
func TestCloudSQLGetLogFilter_DoesNotDoublePrefixProject(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	svc := &cloudSQLService{}

	got := svc.GetLogFilter(ctx, providers.Account{}, "example-project:sample-db")
	if strings.Contains(got, `database_id="example-project:example-project:`) {
		t.Fatalf("project id was doubled into the filter: %s", got)
	}
}
