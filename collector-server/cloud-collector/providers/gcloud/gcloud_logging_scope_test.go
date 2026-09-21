package gcloud

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
)

// A native log alert must not be scoped by the mapped per-service filter. The per-service
// filter drops the policy condition that selected the entries, AND a non-empty result
// short-circuits resolveGcloudScope, so step 0 (alertPolicies.get) never runs. That is how
// a GKE audit-log alert about one Secret modification ended up querying every k8s_cluster
// log line in the project and returning no usable evidence (#33166 residual).
func TestUsePerServiceLogFilter(t *testing.T) {
	tests := []struct {
		name  string
		query providers.QueryLogsRequest
		want  bool
		why   string
	}{
		{
			name: "native log alert skips the per-service filter",
			query: providers.QueryLogsRequest{
				ServiceName: "Kubernetes Engine",
				ResourceId:  "example-cluster",
				AlertType:   "log",
				PolicyID:    "projects/example-project/alertPolicies/1234567890",
			},
			want: false,
			why:  "the policy's own log-match filter must win, via resolveGcloudScope step 0",
		},
		{
			name: "plain metric alert on a mapped service still uses it",
			query: providers.QueryLogsRequest{
				ServiceName: "Kubernetes Engine",
				ResourceId:  "example-cluster",
				AlertType:   "metric",
			},
			want: true,
			why:  "unchanged behaviour for the alert kinds that already worked",
		},
		{
			name: "alert_type log but no policy id falls back to the per-service filter",
			query: providers.QueryLogsRequest{
				ServiceName: "Kubernetes Engine",
				ResourceId:  "example-cluster",
				AlertType:   "log",
			},
			want: true,
			why:  "without a policy id there is no policy filter to resolve, so scope by service",
		},
		{
			name: "explicit log group always wins",
			query: providers.QueryLogsRequest{
				LogGroupName: "projects/p/logs/cloudaudit.googleapis.com%2Factivity",
				ServiceName:  "Kubernetes Engine",
				AlertType:    "log",
				PolicyID:     "projects/p/alertPolicies/1",
			},
			want: false,
			why:  "a caller-supplied log group is authoritative and pre-dates this change",
		},
		{
			name:  "no service name, nothing to resolve",
			query: providers.QueryLogsRequest{ResourceId: "example-cluster"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, usePerServiceLogFilter(tt.query), tt.why)
		})
	}
}
