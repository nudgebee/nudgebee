package gcloud

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sqladmin "google.golang.org/api/sqladmin/v1"
)

// Recommendation evidence must show the bare Cloud SQL instance name, while
// Resource.Id / Recommendation.ResourceId keep the "<project>:<instance>"
// tuple that GCP Monitoring filters and the apply-command path depend on.
func TestGetRecommendations_InstanceIDWithoutProjectPrefix(t *testing.T) {
	s := &cloudSQLService{}

	resource := s.instanceToResource(&sqladmin.DatabaseInstance{
		Name:   "orders-pg",
		Region: "us-central1",
		State:  "RUNNABLE",
		Settings: &sqladmin.Settings{
			ActivationPolicy: "NEVER", // stopped, so the inactive-instance rec fires
		},
	}, "acme-project")

	require.Equal(t, "acme-project:orders-pg", resource.Id)
	require.Empty(t, resource.Tags) // no labels, so the no-labels rec fires

	ctx := providers.NewCloudProviderContext(context.Background())
	recs, err := s.GetRecommendations(ctx, providers.Account{AccountNumber: "acme-project"},
		providers.ListRecommendationsRequest{}, []providers.Resource{resource})
	require.NoError(t, err)
	require.NotEmpty(t, recs)

	for _, rec := range recs {
		assert.Equal(t, "orders-pg", rec.Data["instance_id"],
			"rule %s: evidence instance_id must not carry the project prefix", rec.RuleName)
		assert.Equal(t, "acme-project:orders-pg", rec.ResourceId,
			"rule %s: ResourceId must keep the project-qualified form", rec.RuleName)
	}
}
