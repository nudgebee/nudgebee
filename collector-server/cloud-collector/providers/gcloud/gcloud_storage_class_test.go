package gcloud

import (
	"context"
	"errors"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAutoclassCandidate(t *testing.T) {
	bucket := func(attrs storage.BucketAttrs) providers.Resource {
		attrs.Name = "b"
		return (&cloudStorageService{}).bucketToResource(&attrs, "acme-project")
	}
	lifecycle := func(rule storage.LifecycleRule) storage.Lifecycle {
		return storage.Lifecycle{Rules: []storage.LifecycleRule{rule}}
	}

	assert.True(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD"})))
	assert.True(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD", Autoclass: &storage.Autoclass{Enabled: false}})))
	assert.True(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD", Lifecycle: lifecycle(storage.LifecycleRule{
		Action:    storage.LifecycleAction{Type: storage.DeleteAction},
		Condition: storage.LifecycleCondition{AgeInDays: 90},
	})})))

	assert.False(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "NEARLINE"})), "not Standard")
	assert.False(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD", Autoclass: &storage.Autoclass{Enabled: true}})), "Autoclass already on")
	assert.False(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD", Lifecycle: lifecycle(storage.LifecycleRule{
		Action:    storage.LifecycleAction{Type: storage.SetStorageClassAction, StorageClass: "NEARLINE"},
		Condition: storage.LifecycleCondition{AgeInDays: 30},
	})})), "lifecycle already changes classes")
	assert.False(t, isAutoclassCandidate(bucket(storage.BucketAttrs{StorageClass: "STANDARD", Lifecycle: lifecycle(storage.LifecycleRule{
		Action:    storage.LifecycleAction{Type: storage.DeleteAction},
		Condition: storage.LifecycleCondition{MatchesStorageClasses: []string{"STANDARD"}},
	})})), "lifecycle matches classes, which Autoclass refuses")
}

func TestGcsBucketStorageRate(t *testing.T) {
	bucket := func(attrs storage.BucketAttrs) providers.Resource {
		attrs.Name = "b"
		return (&cloudStorageService{}).bucketToResource(&attrs, "acme-project")
	}

	rate, ok := gcsBucketStorageRate(bucket(storage.BucketAttrs{Location: "US-CENTRAL1", LocationType: "region"}))
	require.True(t, ok)
	assert.Equal(t, gcsStorageRate{Standard: 0.02, Nearline: 0.01}, rate)

	rate, ok = gcsBucketStorageRate(bucket(storage.BucketAttrs{Location: "EU", LocationType: "multi-region"}))
	require.True(t, ok)
	assert.Equal(t, gcsStorageRate{Standard: 0.026, Nearline: 0.015}, rate)

	_, ok = gcsBucketStorageRate(bucket(storage.BucketAttrs{Location: "US", LocationType: "dual-region",
		CustomPlacementConfig: &storage.CustomPlacementConfig{DataLocations: []string{"US-CENTRAL1", "US-WEST1"}}}))
	assert.False(t, ok, "a custom dual-region is billed per region")

	_, ok = gcsBucketStorageRate(bucket(storage.BucketAttrs{Location: "MARS-NORTH1", LocationType: "region"}))
	assert.False(t, ok)

	assert.Equal(t, gcsClassAPer1000RegionalOps, gcsClassAPer1000Ops(bucket(storage.BucketAttrs{LocationType: "region"})))
	assert.Equal(t, gcsClassAPer1000MultiRegionOps, gcsClassAPer1000Ops(bucket(storage.BucketAttrs{LocationType: "multi-region"})))
	assert.Equal(t, gcsClassAPer1000MultiRegionOps, gcsClassAPer1000Ops(bucket(storage.BucketAttrs{})))
}

func storageClassTestBucket(name, location, locationType string) providers.Resource {
	resource := (&cloudStorageService{}).bucketToResource(&storage.BucketAttrs{
		Name:         name,
		Location:     location,
		LocationType: locationType,
		StorageClass: "STANDARD",
	}, "acme-project")
	return resource
}

func fixedUsage(usage map[string]*gcsBucketUsage) gcsBucketUsageQuery {
	return func(start, end time.Time) (map[string]*gcsBucketUsage, error) {
		return usage, nil
	}
}

func TestGcsStorageClassRecommendations(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	const gib = float64(bytesPerGiB)

	cold := storageClassTestBucket("cold-archive", "US-CENTRAL1", "region")
	hot := storageClassTestBucket("hot-assets", "US-CENTRAL1", "region")
	mixed := storageClassTestBucket("already-tiered", "US-CENTRAL1", "region")
	tiny := storageClassTestBucket("many-small-files", "US-CENTRAL1", "region")
	slow := storageClassTestBucket("slow-payback", "US-CENTRAL1", "region")
	empty := storageClassTestBucket("empty", "US-CENTRAL1", "region")
	unpriced := storageClassTestBucket("mars-bucket", "MARS-NORTH1", "region")

	recs := gcsStorageClassRecommendations(ctx, []providers.Resource{cold, hot, mixed, tiny, slow, empty, unpriced}, fixedUsage(map[string]*gcsBucketUsage{
		"cold-archive":     {StandardBytes: 1000 * gib, ObjectCount: 10000},
		"hot-assets":       {StandardBytes: 1000 * gib, ObjectCount: 10000, ReadOps: 50000},
		"already-tiered":   {StandardBytes: 1000 * gib, OtherClassBytes: 10 * gib, ObjectCount: 10000},
		"many-small-files": {StandardBytes: 100 * gib, ObjectCount: 5_000_000},
		// 10M objects: $25/month fee, $50 to enable. Storage saves $27, so $2 net
		// pays the enablement back in 25 months.
		"slow-payback": {StandardBytes: 2700*gib + 10_000_000*gcsAutoclassMinObjectBytes, ObjectCount: 10_000_000},
		"mars-bucket":  {StandardBytes: 1000 * gib, ObjectCount: 10000},
	}), now)

	byBucket := map[string]providers.Recommendation{}
	for _, rec := range recs {
		byBucket[rec.ResourceId] = rec
	}
	require.Len(t, byBucket, 2, "only the cold bucket and the unpriced one are recommended")

	rec := byBucket["cold-archive"]
	assert.Equal(t, constants.GCPStorageClassOptimization, rec.RuleName)
	assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
	assert.False(t, rec.Unevaluated)
	// 1000 GiB less 128 KiB for each of 10,000 objects, all unread, at $0.01/GiB
	// saved, less $0.025 of management fee.
	eligible := 1000 - 10000*128.0/(1024*1024)
	assert.InDelta(t, eligible*0.01-0.025, rec.Savings, 1e-9)
	assert.Equal(t, "enable_autoclass", rec.Data["recommended_action"])
	assert.Equal(t, "NEARLINE", rec.Data["terminal_storage_class"])
	assert.Equal(t, 0.02, rec.Data["standard_rate"])
	assert.Equal(t, 0.01, rec.Data["nearline_rate"])
	assert.Equal(t, 0.03, rec.Data["management_fee_monthly"])
	assert.Equal(t, 0.05, rec.Data["enablement_charge"])
	assert.Equal(t, 0.0, rec.Data["payback_months"])
	assert.Equal(t, round2(eligible), rec.Data["eligible_gib"])
	assert.NotContains(t, rec.Data, "recommended_classes")

	rec = byBucket["mars-bucket"]
	assert.Equal(t, 0.0, rec.Savings, "a location without a price is recommended unpriced")
	assert.Equal(t, "enable_autoclass", rec.Data["recommended_action"])
	assert.NotContains(t, rec.Data, "standard_rate")
}

func TestGcsStorageClassRecommendationsScalesByReads(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	bucket := storageClassTestBucket("half-read", "US-CENTRAL1", "region")
	const gib = float64(bytesPerGiB)

	recs := gcsStorageClassRecommendations(ctx, []providers.Resource{bucket}, fixedUsage(map[string]*gcsBucketUsage{
		"half-read": {StandardBytes: 1000 * gib, ObjectCount: 10000, ReadOps: 5000},
	}), time.Now())

	require.Len(t, recs, 1)
	eligible := (1000 - 10000*128.0/(1024*1024)) * 0.5
	assert.InDelta(t, eligible*0.01-0.025, recs[0].Savings, 1e-9)
}

func TestGcsStorageClassRecommendationsUnevaluatedOnQueryError(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	buckets := []providers.Resource{
		storageClassTestBucket("a", "US-CENTRAL1", "region"),
		storageClassTestBucket("b", "EU", "multi-region"),
	}

	recs := gcsStorageClassRecommendations(ctx, buckets, func(start, end time.Time) (map[string]*gcsBucketUsage, error) {
		return nil, errors.New("permission denied")
	}, time.Now())

	require.Len(t, recs, 2)
	for i, rec := range recs {
		assert.True(t, rec.Unevaluated)
		assert.Equal(t, constants.GCPStorageClassOptimization, rec.RuleName)
		assert.Equal(t, providers.RecommendationCategoryRightSizing, rec.CategoryName)
		assert.Equal(t, buckets[i].Id, rec.ResourceId)
	}
}

func TestGcsStorageClassRecommendationsSkipsQueryWithoutCandidates(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	recs := gcsStorageClassRecommendations(ctx, nil, func(start, end time.Time) (map[string]*gcsBucketUsage, error) {
		t.Fatal("usage must not be read when no bucket is a candidate")
		return nil, nil
	}, time.Now())
	assert.Empty(t, recs)
}

func TestGcsStorageClassRecommendationsLookback(t *testing.T) {
	ctx := providers.NewCloudProviderContext(context.Background())
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	var gotStart, gotEnd time.Time
	gcsStorageClassRecommendations(ctx, []providers.Resource{storageClassTestBucket("a", "US-CENTRAL1", "region")},
		func(start, end time.Time) (map[string]*gcsBucketUsage, error) {
			gotStart, gotEnd = start, end
			return nil, nil
		}, now)
	assert.Equal(t, now.Add(-30*24*time.Hour), gotStart)
	assert.Equal(t, now, gotEnd)
}
