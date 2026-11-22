package gcloud

import (
	"fmt"
	"math"
	"slices"
	"time"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The storage class rule recommends turning on Autoclass for a Standard bucket.
// Autoclass moves each object that is not read for 30 days to Nearline (its default
// terminal class) and moves it back to Standard when it is read, with no retrieval or
// early deletion fees. It charges a management fee per 1,000 objects of 128 KiB or
// more each month, and enabling it on an existing bucket costs one Class A operation
// per object. Figures are from the Cloud Storage pricing and Autoclass pages.
const (
	gcsUsageLookback                    = 30 * 24 * time.Hour
	gcsUsageAlignment                   = 24 * time.Hour
	gcsAutoclassMinObjectBytes          = 128 * 1024
	gcsAutoclassFeePer1000Objects       = 0.0025
	gcsClassAPer1000RegionalOps         = 0.005
	gcsClassAPer1000MultiRegionOps      = 0.01
	gcsAutoclassMaxPaybackMonths        = 12
	gcsAutoclassTerminalStorageClass    = "NEARLINE"
	gcsRecommendedActionEnableAutoclass = "enable_autoclass"
	bytesPerGiB                         = 1 << 30
)

// gcsStorageRate is the USD price per GiB-month of Standard and Nearline storage in
// one location.
type gcsStorageRate struct {
	Standard float64
	Nearline float64
}

// gcsStorageRates holds the Standard and Nearline list prices per location, keyed by
// the lowercased bucket location, as published in the Cloud Billing Catalog for the
// Cloud Storage service in October 2026. Autoclass storage is billed at the same
// rates. Custom dual-regions are billed per region and have no entry.
var gcsStorageRates = map[string]gcsStorageRate{
	// Multi-regions
	"us":   {0.026, 0.015},
	"eu":   {0.026, 0.015},
	"asia": {0.026, 0.015},
	// Predefined dual-regions
	"asia1": {0.0506, 0.0352},
	"eur4":  {0.044, 0.022},
	"eur5":  {0.0473, 0.0253},
	"eur7":  {0.0506, 0.0286},
	"eur8":  {0.0528, 0.0297},
	"nam4":  {0.044, 0.022},
	// Regions
	"africa-south1":           {0.02, 0.01},
	"asia-east1":              {0.02, 0.01},
	"asia-east2":              {0.023, 0.016},
	"asia-northeast1":         {0.023, 0.016},
	"asia-northeast2":         {0.023, 0.016},
	"asia-northeast3":         {0.023, 0.016},
	"asia-south1":             {0.023, 0.016},
	"asia-south2":             {0.023, 0.016},
	"asia-southeast1":         {0.02, 0.01},
	"asia-southeast2":         {0.023, 0.016},
	"asia-southeast3":         {0.021, 0.013},
	"asia-southeast4":         {0.021, 0.013},
	"australia-southeast1":    {0.023, 0.016},
	"australia-southeast2":    {0.023, 0.016},
	"europe-central2":         {0.023, 0.013},
	"europe-north1":           {0.02, 0.01},
	"europe-north2":           {0.023, 0.013},
	"europe-southwest1":       {0.023, 0.0115},
	"europe-west1":            {0.02, 0.01},
	"europe-west10":           {0.025, 0.014},
	"europe-west12":           {0.023, 0.013},
	"europe-west2":            {0.023, 0.013},
	"europe-west3":            {0.023, 0.013},
	"europe-west4":            {0.02, 0.01},
	"europe-west6":            {0.025, 0.014},
	"europe-west8":            {0.023, 0.013},
	"europe-west9":            {0.023, 0.013},
	"me-central1":             {0.023, 0.013},
	"me-central2":             {0.03, 0.018},
	"me-west1":                {0.021, 0.0125},
	"northamerica-northeast1": {0.023, 0.013},
	"northamerica-northeast2": {0.023, 0.013},
	"northamerica-south1":     {0.026, 0.018},
	"southamerica-east1":      {0.035, 0.02},
	"southamerica-west1":      {0.03, 0.018},
	"us-central1":             {0.02, 0.01},
	"us-east1":                {0.02, 0.01},
	"us-east4":                {0.023, 0.013},
	"us-east5":                {0.02, 0.01},
	"us-south1":               {0.02, 0.01},
	"us-west1":                {0.02, 0.01},
	"us-west2":                {0.023, 0.016},
	"us-west3":                {0.023, 0.016},
	"us-west4":                {0.023, 0.013},
	"us-west8":                {0.02, 0.01},
}

// gcsStandardStorageClasses are the storage_class label values Cloud Monitoring
// reports Standard storage under: REGIONAL in a region, MULTI_REGIONAL otherwise.
var gcsStandardStorageClasses = map[string]bool{"STANDARD": true, "REGIONAL": true, "MULTI_REGIONAL": true}

// gcsBucketUsage is what Cloud Monitoring reports for one bucket over the lookback.
type gcsBucketUsage struct {
	StandardBytes   float64 // smallest Standard size seen, summed over Standard labels
	OtherClassBytes float64 // largest size seen in any other class
	ObjectCount     float64 // largest object count seen, all classes
	ReadOps         float64 // ReadObject calls
}

// gcsBucketUsageQuery reads the usage of every bucket in the project between start
// and end, keyed by bucket name.
type gcsBucketUsageQuery func(start, end time.Time) (map[string]*gcsBucketUsage, error)

// isAutoclassCandidate reports whether a bucket is one Autoclass could be turned on
// for: Standard by default, Autoclass off, and no lifecycle rule that changes or
// matches storage classes, which Autoclass refuses to coexist with.
func isAutoclassCandidate(resource providers.Resource) bool {
	if storageClass, _ := resource.Meta["StorageClass"].(string); storageClass != "STANDARD" {
		return false
	}
	if autoclass, ok := resource.Meta["Autoclass"].(map[string]interface{}); ok {
		if enabled, _ := autoclass["Enabled"].(bool); enabled {
			return false
		}
	}
	lifecycle, _ := resource.Meta["Lifecycle"].(map[string]interface{})
	rules, _ := lifecycle["Rules"].([]interface{})
	for _, rule := range rules {
		rule, _ := rule.(map[string]interface{})
		action, _ := rule["Action"].(map[string]interface{})
		if actionType, _ := action["Type"].(string); actionType == "SetStorageClass" {
			return false
		}
		condition, _ := rule["Condition"].(map[string]interface{})
		if classes, _ := condition["MatchesStorageClasses"].([]interface{}); len(classes) > 0 {
			return false
		}
	}
	return true
}

// gcsBucketStorageRate returns the storage prices for a bucket's location. A custom
// dual-region is billed per underlying region and has no rate.
func gcsBucketStorageRate(resource providers.Resource) (gcsStorageRate, bool) {
	if _, ok := resource.Meta["CustomPlacementConfig"].(map[string]interface{}); ok {
		return gcsStorageRate{}, false
	}
	rate, ok := gcsStorageRates[resource.Region]
	return rate, ok
}

// gcsClassAPer1000Ops is the Standard Class A operation price for a bucket's location
// type. An unknown location type takes the higher, multi-region price.
func gcsClassAPer1000Ops(resource providers.Resource) float64 {
	if locationType, _ := resource.Meta["LocationType"].(string); locationType == "region" {
		return gcsClassAPer1000RegionalOps
	}
	return gcsClassAPer1000MultiRegionOps
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

// gcsStorageClassRecommendations recommends turning on Autoclass for the candidate
// buckets that would save money doing so. Usage comes from query; if it cannot be
// read, each bucket is returned as an unevaluated placeholder so a recommendation it
// already has is left as it is.
//
// The saving is the Standard bytes Autoclass would move to Nearline, priced at the
// difference between the two classes, less the management fee. It is kept low on
// purpose: the smallest Standard size of the lookback is used; up to 128 KiB per
// object is taken off, since smaller objects never move; and the rest is scaled down
// by the share of objects that reads could have kept in Standard. Buckets that hold
// any other class are skipped, because enabling Autoclass moves those objects back to
// Standard. Buckets whose one-time enablement charge would take more than
// gcsAutoclassMaxPaybackMonths to recover are skipped.
func gcsStorageClassRecommendations(ctx providers.CloudProviderContext, buckets []providers.Resource, query gcsBucketUsageQuery, now time.Time) []providers.Recommendation {
	if len(buckets) == 0 {
		return nil
	}
	var recommendations []providers.Recommendation
	usage, err := query(now.Add(-gcsUsageLookback), now)
	if err != nil {
		ctx.GetLogger().Warn("gcp: unable to read cloud storage usage, storage class rule not evaluated", "error", err, "buckets", len(buckets))
		for _, bucket := range buckets {
			recommendations = append(recommendations, providers.UnevaluatedRecommendation(
				constants.GCPStorageClassOptimization, providers.RecommendationCategoryRightSizing, bucket))
		}
		return recommendations
	}

	for _, bucket := range buckets {
		u := usage[bucket.Id]
		// Cloud Monitoring does not track empty buckets.
		if u == nil || u.StandardBytes <= 0 || u.ObjectCount <= 0 || u.OtherClassBytes > 0 {
			continue
		}
		smallObjectBytes := u.ObjectCount * gcsAutoclassMinObjectBytes
		coldShare := 1 - min(1, u.ReadOps/u.ObjectCount)
		eligibleGiB := max(0, u.StandardBytes-smallObjectBytes) * coldShare / bytesPerGiB
		if eligibleGiB <= 0 {
			continue
		}

		data := map[string]any{
			"bucket_id":              bucket.Id,
			"bucket_name":            bucket.Name,
			"region":                 bucket.Region,
			"current_storage_class":  bucket.Meta["StorageClass"],
			"recommended_action":     gcsRecommendedActionEnableAutoclass,
			"terminal_storage_class": gcsAutoclassTerminalStorageClass,
			"lookback_days":          int(gcsUsageLookback.Hours() / 24),
			"standard_bytes":         u.StandardBytes,
			"object_count":           u.ObjectCount,
			"read_ops":               u.ReadOps,
			"eligible_gib":           round2(eligibleGiB),
		}

		savings := 0.0
		if rate, ok := gcsBucketStorageRate(bucket); ok {
			fee := u.ObjectCount / 1000 * gcsAutoclassFeePer1000Objects
			savings = eligibleGiB*(rate.Standard-rate.Nearline) - fee
			enablement := u.ObjectCount / 1000 * gcsClassAPer1000Ops(bucket)
			if savings <= 0 || enablement/savings > gcsAutoclassMaxPaybackMonths {
				continue
			}
			data["standard_rate"] = rate.Standard
			data["nearline_rate"] = rate.Nearline
			data["management_fee_monthly"] = round2(fee)
			data["enablement_charge"] = round2(enablement)
			data["payback_months"] = math.Round(enablement/savings*10) / 10
		} else {
			ctx.GetLogger().Info("gcp: no storage price for bucket location, storage class recommendation not priced", "bucket", bucket.Id, "location", bucket.Region)
		}

		recommendations = append(recommendations, providers.Recommendation{
			CategoryName:        providers.RecommendationCategoryRightSizing,
			RuleName:            constants.GCPStorageClassOptimization,
			Severity:            providers.RecommendationSeverityMedium,
			Savings:             savings,
			Data:                data,
			Action:              providers.RecommendationActionModify,
			ResourceServiceName: bucket.ServiceName,
			ResourceId:          bucket.Id,
			ResourceType:        bucket.Type,
			ResourceRegion:      bucket.Region,
		})
	}
	return recommendations
}

// storageClassRecommendations runs the storage class rule over the candidate buckets,
// creating the Cloud Monitoring client only if there is a bucket to read usage for.
func (s *cloudStorageService) storageClassRecommendations(ctx providers.CloudProviderContext, account providers.Account, buckets []providers.Resource) []providers.Recommendation {
	query := func(start, end time.Time) (map[string]*gcsBucketUsage, error) {
		session, err := getGcloudSessionFromAccount(ctx, account)
		if err != nil {
			return nil, fmt.Errorf("failed to get gcloud session: %w", err)
		}
		client, err := monitoring.NewMetricClient(ctx.GetContext(), session.Opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create monitoring client: %w", err)
		}
		defer func() {
			if cerr := client.Close(); cerr != nil {
				ctx.GetLogger().Error("failed to close monitoring client", "error", cerr)
			}
		}()
		return readGcsBucketUsage(ctx, client, session.ProjectId, start, end)
	}
	return gcsStorageClassRecommendations(ctx, buckets, query, time.Now().UTC())
}

// readGcsBucketUsage reads the usage of every bucket in a project, one Cloud
// Monitoring query per metric, each returning a value per day for every bucket (and
// storage class). Sizes and counts are measured once a day.
func readGcsBucketUsage(ctx providers.CloudProviderContext, client *monitoring.MetricClient, project string, start, end time.Time) (map[string]*gcsBucketUsage, error) {
	usage := map[string]*gcsBucketUsage{}
	bucketUsage := func(series *monitoringpb.TimeSeries) *gcsBucketUsage {
		name := series.GetResource().GetLabels()["bucket_name"]
		if usage[name] == nil {
			usage[name] = &gcsBucketUsage{}
		}
		return usage[name]
	}

	queries := []struct {
		metric  string
		filter  string
		aligner monitoringpb.Aggregation_Aligner
		groupBy []string
		apply   func(series *monitoringpb.TimeSeries, values []float64)
	}{
		{
			metric:  "storage/total_bytes",
			aligner: monitoringpb.Aggregation_ALIGN_MEAN,
			groupBy: []string{"resource.label.bucket_name", "metric.label.storage_class"},
			apply: func(series *monitoringpb.TimeSeries, values []float64) {
				u := bucketUsage(series)
				if gcsStandardStorageClasses[series.GetMetric().GetLabels()["storage_class"]] {
					u.StandardBytes += slices.Min(values)
				} else {
					u.OtherClassBytes = max(u.OtherClassBytes, slices.Max(values))
				}
			},
		},
		{
			metric:  "storage/object_count",
			aligner: monitoringpb.Aggregation_ALIGN_MEAN,
			groupBy: []string{"resource.label.bucket_name"},
			apply: func(series *monitoringpb.TimeSeries, values []float64) {
				bucketUsage(series).ObjectCount = slices.Max(values)
			},
		},
		{
			metric:  "api/request_count",
			filter:  ` AND metric.labels.method = "ReadObject"`,
			aligner: monitoringpb.Aggregation_ALIGN_SUM,
			groupBy: []string{"resource.label.bucket_name"},
			apply: func(series *monitoringpb.TimeSeries, values []float64) {
				total := 0.0
				for _, value := range values {
					total += value
				}
				bucketUsage(series).ReadOps = total
			},
		},
	}

	for _, q := range queries {
		it := client.ListTimeSeries(ctx.GetContext(), &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + project,
			Filter: fmt.Sprintf(`metric.type = "storage.googleapis.com/%s" AND resource.type = "gcs_bucket"%s`, q.metric, q.filter),
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(start),
				EndTime:   timestamppb.New(end),
			},
			Aggregation: &monitoringpb.Aggregation{
				AlignmentPeriod:    durationpb.New(gcsUsageAlignment),
				PerSeriesAligner:   q.aligner,
				CrossSeriesReducer: monitoringpb.Aggregation_REDUCE_SUM,
				GroupByFields:      q.groupBy,
			},
		})
		for {
			series, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				return nil, fmt.Errorf("failed to read %s: %w", q.metric, err)
			}
			var values []float64
			for _, point := range series.GetPoints() {
				switch v := point.GetValue().GetValue().(type) {
				case *monitoringpb.TypedValue_Int64Value:
					values = append(values, float64(v.Int64Value))
				case *monitoringpb.TypedValue_DoubleValue:
					values = append(values, v.DoubleValue)
				}
			}
			if len(values) > 0 {
				q.apply(series, values)
			}
		}
	}
	return usage, nil
}
