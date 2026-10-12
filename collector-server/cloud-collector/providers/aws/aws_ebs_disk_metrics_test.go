package aws

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testEbsWindowEnd   = time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC)
	testEbsWindowStart = testEbsWindowEnd.Add(-providers.DiskObservationWindow)
)

func ebsTestTimes(n ...int) []time.Time {
	times := make([]time.Time, len(n))
	for i, step := range n {
		times[i] = testEbsWindowStart.Add(time.Duration(step) * providers.DiskMetricPeriod)
	}
	return times
}

func TestEbsDiskMetricQueries(t *testing.T) {
	queries := ebsDiskMetricQueries([]providers.DiskConfig{
		{DiskID: "vol-1", AttachedTo: []string{"i-1"}},
		{DiskID: "vol-2", AttachedTo: []string{"i-2"}},
	})
	require.Len(t, queries, 12)

	validID := regexp.MustCompile(`^[a-z][a-zA-Z0-9_]*$`)
	ids := map[string]bool{}
	labels := map[string]providers.CloudWatchMetricEntry{}
	for _, query := range queries {
		assert.Regexp(t, validID, query.Id)
		assert.False(t, ids[query.Id], "duplicate query id %s", query.Id)
		ids[query.Id] = true

		require.NotNil(t, query.MetricStat)
		assert.Empty(t, query.Expression)
		assert.Equal(t, "AWS/EBS", query.MetricStat.Metric.Namespace)
		labels[query.Label] = query
	}
	require.Len(t, labels, 12)

	for _, volumeID := range []string{"vol-1", "vol-2"} {
		for _, metric := range []string{"VolumeReadOps", "VolumeWriteOps", "VolumeReadBytes", "VolumeWriteBytes"} {
			query, ok := labels[volumeID+"|"+metric]
			require.True(t, ok, "missing query for %s %s", volumeID, metric)
			assert.Equal(t, metric, query.MetricStat.Metric.Name)
			assert.Equal(t, map[string]string{"VolumeId": volumeID}, query.MetricStat.Metric.Dimensions)
			// The busiest minute of each five-minute interval, not the interval's total.
			assert.Equal(t, "Maximum", query.MetricStat.Stat)
			assert.Equal(t, 300, query.MetricStat.Period)
		}
		for _, metric := range []string{"VolumeIOPSExceededCheck", "VolumeThroughputExceededCheck"} {
			query, ok := labels[volumeID+"|"+metric]
			require.True(t, ok, "missing query for %s %s", volumeID, metric)
			// The flags are published per attachment, under both dimensions.
			instanceID := "i-" + strings.TrimPrefix(volumeID, "vol-")
			assert.Equal(t, map[string]string{"VolumeId": volumeID, "InstanceId": instanceID}, query.MetricStat.Metric.Dimensions)
			assert.Equal(t, "Maximum", query.MetricStat.Stat)
			assert.Equal(t, 86400, query.MetricStat.Period)
		}
	}
}

// The label is the only thing tying a result back to its volume and metric, so it
// has to survive the conversion getAwsCloudwatchMetrics applies to these queries.
func TestEbsDiskMetricQueriesSurviveCloudWatchConversion(t *testing.T) {
	sdkQueries, queryIDMap := buildMetricDataQueriesFromMetricQueries(
		ebsDiskMetricQueries([]providers.DiskConfig{{DiskID: "vol-1", AttachedTo: []string{"i-1"}}}), "123456789012")
	require.Len(t, sdkQueries, 6)

	first := sdkQueries[0]
	assert.Equal(t, "123456789012", aws.ToString(first.AccountId))
	require.NotNil(t, first.MetricStat)
	assert.Equal(t, "AWS/EBS", aws.ToString(first.MetricStat.Metric.Namespace))
	assert.Equal(t, "VolumeReadOps", aws.ToString(first.MetricStat.Metric.MetricName))
	assert.Equal(t, int32(300), aws.ToInt32(first.MetricStat.Period))
	assert.Equal(t, "Maximum", aws.ToString(first.MetricStat.Stat))
	require.Len(t, first.MetricStat.Metric.Dimensions, 1)
	assert.Equal(t, "VolumeId", aws.ToString(first.MetricStat.Metric.Dimensions[0].Name))
	assert.Equal(t, "vol-1", aws.ToString(first.MetricStat.Metric.Dimensions[0].Value))

	assert.Equal(t, "vol-1|VolumeReadOps", queryIDMap[aws.ToString(first.Id)].metricName)
	assert.Equal(t, "vol-1|VolumeThroughputExceededCheck", queryIDMap[aws.ToString(sdkQueries[5].Id)].metricName)
}

func TestEbsDiskSeriesFromItems(t *testing.T) {
	items := []providers.MetricItem{
		// vol-1 read ops arrive as two pages, newest first, as GetMetricData returns them.
		{Name: "vol-1|VolumeReadOps", Statistics: "Sum", Values: []float64{900, 600}, Timestamps: ebsTestTimes(3, 2)},
		{Name: "vol-1|VolumeReadOps", Statistics: "Sum", Values: []float64{300, 0}, Timestamps: ebsTestTimes(1, 0)},
		{Name: "vol-1|VolumeWriteOps", Statistics: "Sum", Values: []float64{1500}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1|VolumeReadBytes", Statistics: "Sum", Values: []float64{1048576}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1|VolumeWriteBytes", Statistics: "Sum", Values: []float64{2097152}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1|VolumeIOPSExceededCheck", Statistics: "Maximum", Values: []float64{1}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-2|VolumeReadOps", Statistics: "Sum", Values: []float64{7}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-9|VolumeReadOps", Statistics: "Sum", Values: []float64{123}, Timestamps: ebsTestTimes(0)},
	}

	series := ebsDiskSeriesFromItems([]string{"vol-1", "vol-2", "vol-3"}, items, testEbsWindowStart, testEbsWindowEnd)
	require.Len(t, series, 3)

	vol1 := series[0]
	assert.Equal(t, "vol-1", vol1.DiskID)
	assert.Equal(t, testEbsWindowStart, vol1.Start)
	assert.Equal(t, testEbsWindowEnd, vol1.End)
	assert.Equal(t, int64(300), vol1.PeriodSeconds)
	// Each value is the total of one minute, so that is what it is divided by.
	assert.Equal(t, int64(60), vol1.SampleSeconds)
	assert.False(t, vol1.ValuesAreRates)
	require.NotNil(t, vol1.ReadOps)
	assert.Equal(t, "VolumeReadOps", vol1.ReadOps.Name)
	assert.Equal(t, "vol-1", vol1.ReadOps.ResourceId)
	assert.Equal(t, []float64{0, 300, 600, 900}, vol1.ReadOps.Values)
	assert.Equal(t, ebsTestTimes(0, 1, 2, 3), vol1.ReadOps.Timestamps)
	require.NotNil(t, vol1.WriteOps)
	assert.Equal(t, []float64{1500}, vol1.WriteOps.Values)
	require.NotNil(t, vol1.ReadBytes)
	assert.Equal(t, []float64{1048576}, vol1.ReadBytes.Values)
	require.NotNil(t, vol1.WriteBytes)
	assert.Equal(t, []float64{2097152}, vol1.WriteBytes.Values)
	require.NotNil(t, vol1.IOPSExceeded)
	assert.Equal(t, []float64{1}, vol1.IOPSExceeded.Values)
	assert.Nil(t, vol1.ThroughputExceeded)

	// vol-2 gets only its own series, and what was not returned stays nil, not zero.
	vol2 := series[1]
	assert.Equal(t, "vol-2", vol2.DiskID)
	require.NotNil(t, vol2.ReadOps)
	assert.Equal(t, []float64{7}, vol2.ReadOps.Values)
	assert.Nil(t, vol2.WriteOps)
	assert.Nil(t, vol2.ReadBytes)
	assert.Nil(t, vol2.WriteBytes)

	// vol-3 had nothing returned at all.
	vol3 := series[2]
	assert.Equal(t, "vol-3", vol3.DiskID)
	assert.Nil(t, vol3.ReadOps)
	assert.Nil(t, vol3.WriteOps)
	assert.Nil(t, vol3.ReadBytes)
	assert.Nil(t, vol3.WriteBytes)
	assert.Nil(t, vol3.IOPSExceeded)
	assert.Nil(t, vol3.ThroughputExceeded)
}

func ebsTestDisks(region string, count int) []providers.DiskConfig {
	disks := make([]providers.DiskConfig, count)
	for i := range disks {
		disks[i] = providers.DiskConfig{DiskID: fmt.Sprintf("vol-%s-%03d", region, i), Region: region, AttachedTo: []string{fmt.Sprintf("i-%03d", i)}}
	}
	return disks
}

func TestFetchEbsDiskSeriesBatchesByRegion(t *testing.T) {
	disks := append(ebsTestDisks("us-east-1", 60), ebsTestDisks("eu-west-1", 3)...)
	disks = append(disks, disks[0]) // a repeated volume is fetched once

	var requests []providers.QueryMetricsRequest
	query := func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		requests = append(requests, filter)
		// Answer with one read-ops point for the first volume of the batch only.
		label := filter.MetricQueries[0].Label
		return providers.QueryMetricsResponse{Items: []providers.MetricItem{
			{Name: label, Statistics: "Sum", Values: []float64{42}, Timestamps: ebsTestTimes(0)},
		}}, nil
	}

	var yielded []providers.DiskPerformanceSeries
	err := fetchEbsDiskSeries(providers.NewCloudProviderContext(context.Background()), providers.Account{AccountNumber: "123456789012"},
		disks, testEbsWindowStart, testEbsWindowEnd, query, func(s providers.DiskPerformanceSeries) { yielded = append(yielded, s) })
	require.NoError(t, err)

	// 60 volumes in us-east-1 make three batches (25, 25, 10); eu-west-1 makes one.
	require.Len(t, requests, 4)
	wantRegions := []string{"us-east-1", "us-east-1", "us-east-1", "eu-west-1"}
	wantVolumes := []int{25, 25, 10, 3}
	for i, request := range requests {
		assert.Equal(t, wantRegions[i], request.Region)
		assert.Len(t, request.MetricQueries, wantVolumes[i]*6)
		assert.LessOrEqual(t, len(request.MetricQueries), maxMetricDataQueries)
		assert.Equal(t, testEbsWindowStart, *request.StartDate)
		assert.Equal(t, testEbsWindowEnd, *request.EndDate)
		assert.Empty(t, request.ServiceName)
		assert.Empty(t, request.ResourceIds)
	}

	// Every volume is yielded exactly once, including those with no data.
	require.Len(t, yielded, 63)
	seen := map[string]int{}
	withData := 0
	for _, series := range yielded {
		seen[series.DiskID]++
		if series.ReadOps != nil {
			withData++
		}
	}
	assert.Len(t, seen, 63)
	assert.Equal(t, 4, withData)
}

func TestFetchEbsDiskSeriesSkipsFailedBatch(t *testing.T) {
	disks := append(ebsTestDisks("us-east-1", 2), ebsTestDisks("eu-west-1", 2)...)
	throttled := errors.New("ThrottlingException: Rate exceeded")

	query := func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		if filter.Region == "us-east-1" {
			return providers.QueryMetricsResponse{}, throttled
		}
		return providers.QueryMetricsResponse{}, nil
	}

	var yielded []string
	err := fetchEbsDiskSeries(providers.NewCloudProviderContext(context.Background()), providers.Account{AccountNumber: "123456789012"},
		disks, testEbsWindowStart, testEbsWindowEnd, query, func(s providers.DiskPerformanceSeries) { yielded = append(yielded, s.DiskID) })

	// The failed region is reported and its volumes are not yielded as if they had
	// no usage; the other region still comes through.
	require.ErrorIs(t, err, throttled)
	assert.Contains(t, err.Error(), "us-east-1")
	assert.Equal(t, []string{"vol-eu-west-1-000", "vol-eu-west-1-001"}, yielded)
}

func TestFetchEbsDiskSeriesNoDisks(t *testing.T) {
	query := func(providers.CloudProviderContext, providers.Account, providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		t.Fatal("no query expected")
		return providers.QueryMetricsResponse{}, nil
	}
	err := fetchEbsDiskSeries(providers.NewCloudProviderContext(context.Background()), providers.Account{}, nil,
		testEbsWindowStart, testEbsWindowEnd, query, func(providers.DiskPerformanceSeries) { t.Fatal("no series expected") })
	assert.NoError(t, err)
}

// A result whose label does not name a requested volume and metric cannot be tied to
// a disk, so it is dropped rather than guessed at.
func TestEbsDiskSeriesFromItemsIgnoresUnrecognisedLabels(t *testing.T) {
	items := []providers.MetricItem{
		{Name: "VolumeReadOps", Values: []float64{111}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1", Values: []float64{222}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1|", Values: []float64{333}, Timestamps: ebsTestTimes(0)},
		{Name: "vol-1|SomeOtherMetric", Values: []float64{444}, Timestamps: ebsTestTimes(0)},
		{Name: "|VolumeReadOps", Values: []float64{555}, Timestamps: ebsTestTimes(0)},
		{Name: "", Values: []float64{666}, Timestamps: ebsTestTimes(0)},
	}

	series := ebsDiskSeriesFromItems([]string{"vol-1"}, items, testEbsWindowStart, testEbsWindowEnd)

	require.Len(t, series, 1)
	assert.Nil(t, series[0].ReadOps)
	assert.Nil(t, series[0].WriteOps)
	assert.Nil(t, series[0].ReadBytes)
	assert.Nil(t, series[0].WriteBytes)
	assert.Nil(t, series[0].IOPSExceeded)
	assert.Nil(t, series[0].ThroughputExceeded)
}

// The exceeded-check flags exist only per attachment, so an unattached volume gets no
// query for them, and a volume attached to several instances gets one pair each.
func TestEbsDiskMetricQueriesExceededChecksFollowAttachments(t *testing.T) {
	count := func(queries []providers.CloudWatchMetricEntry, metric string) int {
		n := 0
		for _, query := range queries {
			if query.MetricStat.Metric.Name == metric {
				n++
			}
		}
		return n
	}

	unattached := ebsDiskMetricQueries([]providers.DiskConfig{{DiskID: "vol-1"}})
	require.Len(t, unattached, 4)
	assert.Zero(t, count(unattached, "VolumeIOPSExceededCheck"))

	shared := ebsDiskMetricQueries([]providers.DiskConfig{{DiskID: "vol-1", AttachedTo: []string{"i-1", "i-2", "i-3"}}})
	require.Len(t, shared, 10)
	assert.Equal(t, 1, count(shared, "VolumeReadOps"))
	assert.Equal(t, 3, count(shared, "VolumeIOPSExceededCheck"))
	assert.Equal(t, 3, count(shared, "VolumeThroughputExceededCheck"))

	ids := map[string]bool{}
	for _, query := range shared {
		assert.False(t, ids[query.Id], "duplicate query id %s", query.Id)
		ids[query.Id] = true
	}
}

// A volume attached to two instances: the flag was raised on the second one only, on
// one day. It must survive being joined with the first instance's all-clear series.
func TestEbsDiskSeriesFromItemsKeepsFlagRaisedOnAnyAttachment(t *testing.T) {
	day := func(n int) time.Time { return testEbsWindowStart.AddDate(0, 0, n) }
	items := []providers.MetricItem{
		{Name: "vol-1|VolumeIOPSExceededCheck", Statistics: "Maximum", Values: []float64{0, 0, 0}, Timestamps: []time.Time{day(2), day(1), day(0)}},
		{Name: "vol-1|VolumeIOPSExceededCheck", Statistics: "Maximum", Values: []float64{0, 1, 0}, Timestamps: []time.Time{day(2), day(1), day(0)}},
		{Name: "vol-1|VolumeThroughputExceededCheck", Statistics: "Maximum", Values: []float64{0, 0}, Timestamps: []time.Time{day(1), day(0)}},
	}

	series := ebsDiskSeriesFromItems([]string{"vol-1"}, items, testEbsWindowStart, testEbsWindowEnd)

	require.Len(t, series, 1)
	require.NotNil(t, series[0].IOPSExceeded)
	assert.Equal(t, "VolumeIOPSExceededCheck", series[0].IOPSExceeded.Name)
	assert.Equal(t, []time.Time{day(0), day(1), day(2)}, series[0].IOPSExceeded.Timestamps)
	assert.Equal(t, []float64{0, 1, 0}, series[0].IOPSExceeded.Values)
	require.NotNil(t, series[0].ThroughputExceeded)
	assert.Equal(t, []float64{0, 0}, series[0].ThroughputExceeded.Values)

	analysis := providers.AnalyzeDiskPerformance(series[0])
	require.NotNil(t, analysis.IOPSLimitExceeded)
	assert.True(t, *analysis.IOPSLimitExceeded)
	require.NotNil(t, analysis.ThroughputLimitExceeded)
	assert.False(t, *analysis.ThroughputLimitExceeded)
}
