package account

import (
	"errors"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/security"
	"os"
	"testing"
	"time"

	_ "nudgebee/collector/cloud/providers/aws"
	_ "nudgebee/collector/cloud/providers/gcloud"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRecommendationsRecordsProviderFailure(t *testing.T) {
	providerErr := errors.New("provider permission denied")
	var recorded map[string]any
	deps := storeRecommendationsDeps{
		getDB: func(common.DatabaseManagerType) (*common.DatabaseManager, error) {
			return &common.DatabaseManager{}, nil
		},
		fetch: func(*security.RequestContext, string, providers.ListRecommendationsRequest) (providers.ListRecommendationsResponse, providers.Account, error) {
			return providers.ListRecommendationsResponse{}, providers.Account{AccountNumber: "123"}, providerErr
		},
		update: func(_ *security.RequestContext, accountID string, status AgentStatus, message string, synced bool, connectionStatus map[string]any) error {
			assert.Equal(t, "acc-1", accountID)
			assert.Equal(t, AgentStatusConnected, status)
			assert.Equal(t, providerErr.Error(), message)
			assert.True(t, synced)
			recorded = connectionStatus
			return nil
		},
	}

	ctx := security.NewRequestContextForTenantAdmin("tenant-1")
	_, err := storeRecommendations(ctx, "acc-1", providers.ListRecommendationsRequest{ServiceName: "AmazonEC2"}, deps)
	require.ErrorIs(t, err, providerErr)
	recommendations := recorded["recommendations"].(map[string]any)
	assert.Equal(t, providerErr.Error(), recommendations["err"])
	assert.NotEmpty(t, recommendations["updated_at"])
}

func TestStoreRecommendationsAwsRDS(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonRDS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsEc2(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonEc2",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsLambda(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSLambda",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAWsECS(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonECS",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsForAwsAllServices(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationAwsIam(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AWSIAM",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsS3(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "AmazonS3",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsSecurityHub(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "SecurityHub",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAwsCloudTrail(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "CloudTrail",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsAzureAll(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, "c3a2d91d-17b7-4df4-93a0-7a777a399e29")
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationsGCloudAll(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendationsAll(ctx, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestStoreRecommendationGCloudStorage(t *testing.T) {
	if os.Getenv("TEST_ACCOUNT") == "" {
		t.Skip("Skipping integration test that requires a Metastore database and TEST_ACCOUNT")
	}
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	response, err := StoreRecommendations(ctx, os.Getenv("TEST_ACCOUNT"), providers.ListRecommendationsRequest{
		ServiceName: "storage",
	})
	assert.Nil(t, err)
	assert.NotEmpty(t, response)
}

func TestResourceCreatedAtPrefersCloudCreationTime(t *testing.T) {
	cloudCreated := time.Date(2025, 9, 26, 10, 0, 0, 0, time.UTC)
	firstStored := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	zero := time.Time{}
	epoch := time.UnixMilli(0).UTC()

	cases := []struct {
		name           string
		cloudCreatedOn *time.Time
		firstStored    *time.Time
		want           *time.Time
	}{
		{"cloud creation time wins over first stored", &cloudCreated, &firstStored, &cloudCreated},
		{"zero creation time falls back to first stored", &zero, &firstStored, &firstStored},
		{"epoch creation time falls back to first stored", &epoch, &firstStored, &firstStored},
		{"missing creation time falls back to first stored", nil, &firstStored, &firstStored},
		{"both missing stays missing", nil, nil, nil},
		{"zero first stored stays missing", nil, &zero, nil},
		{"epoch first stored stays missing", nil, &epoch, nil},
		{"zero creation time and zero first stored stays missing", &zero, &zero, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resourceCreatedAt(tc.cloudCreatedOn, tc.firstStored))
		})
	}
}

func TestSplitUnevaluatedRecommendations(t *testing.T) {
	account := providers.Account{CloudProvider: "AWS", AccountNumber: "123456789012"}
	stored := providers.Recommendation{
		RuleName: "orphaned_volume", ResourceServiceName: "AmazonEC2", ResourceType: "storage", ResourceId: "vol-1", ResourceRegion: "us-east-1", Savings: 10,
	}
	placeholder := providers.Recommendation{
		RuleName: "disk_performance_overprovisioned", Unevaluated: true,
		ResourceServiceName: "AmazonEC2", ResourceType: "storage", ResourceId: "vol-2", ResourceRegion: "us-east-1",
	}

	t.Run("placeholders are taken out and turned into keys", func(t *testing.T) {
		toStore, keys := splitUnevaluatedRecommendations(account, []providers.Recommendation{stored, placeholder})

		assert.Equal(t, []providers.Recommendation{stored}, toStore)
		// The key is the rule name joined to the account object id the row for this
		// rule and resource is stored under.
		accountObjectId := buildExternalResourceId("AWS", "123456789012", "us-east-1", "AmazonEC2", "storage", "vol-2", "")
		assert.Equal(t, []string{"disk_performance_overprovisioned\x1f" + accountObjectId}, keys)
	})

	t.Run("without placeholders nothing changes", func(t *testing.T) {
		items := []providers.Recommendation{stored}
		toStore, keys := splitUnevaluatedRecommendations(account, items)
		assert.Equal(t, items, toStore)
		assert.Nil(t, keys)

		toStore, keys = splitUnevaluatedRecommendations(account, nil)
		assert.Nil(t, toStore)
		assert.Nil(t, keys)
	})

	t.Run("only placeholders leaves nothing to store", func(t *testing.T) {
		toStore, keys := splitUnevaluatedRecommendations(account, []providers.Recommendation{placeholder})
		assert.Empty(t, toStore)
		assert.Len(t, keys, 1)
	})

	t.Run("the same resource under another rule gets a different key", func(t *testing.T) {
		other := placeholder
		other.RuleName = "orphaned_volume"
		_, keys := splitUnevaluatedRecommendations(account, []providers.Recommendation{placeholder, other})
		require.Len(t, keys, 2)
		assert.NotEqual(t, keys[0], keys[1])
	})
}

func TestArchiveOpenRecommendationsQuery(t *testing.T) {
	// The statement every sync has always run, kept byte for byte when no rule
	// reported an unevaluated resource.
	const unchanged = `update recommendation set status = 'Archive' where cloud_account_id = $1 and status = 'Open' and (resource_id in (select id from cloud_resourses where lower(service_name) = $2 and account = $1) or lower((recommendation ->> 'service_name')::varchar) = $2)`

	t.Run("no protected rows", func(t *testing.T) {
		query, args := archiveOpenRecommendationsQuery("acc-1", "AmazonEC2", nil)
		assert.Equal(t, unchanged, query)
		assert.Equal(t, []any{"acc-1", "amazonec2"}, args)
	})

	t.Run("protected rows are excluded from the archive", func(t *testing.T) {
		keys := []string{"disk_performance_overprovisioned\x1fobj-1", "disk_performance_overprovisioned\x1fobj-2"}
		query, args := archiveOpenRecommendationsQuery("acc-1", "AmazonEC2", keys)

		assert.Equal(t, unchanged+` and not ((rule_name || chr(31) || coalesce(account_object_id, '')) = ANY($3::text[]))`, query)
		require.Len(t, args, 3)
		assert.Equal(t, "acc-1", args[0])
		assert.Equal(t, "amazonec2", args[1])
		assert.Equal(t, pq.Array(keys), args[2])
	})
}
