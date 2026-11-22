package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testAzureWindowEnd   = time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC)
	testAzureWindowStart = testAzureWindowEnd.Add(-providers.DiskObservationWindow)
)

func azureTestTimes(n ...int) []time.Time {
	times := make([]time.Time, len(n))
	for i, step := range n {
		times[i] = testAzureWindowStart.Add(time.Duration(step) * providers.DiskMetricPeriod)
	}
	return times
}

func azureTestDiskID(n int) string {
	return fmt.Sprintf("/subscriptions/11111111-2222-3333-4444-555555555555/resourceGroups/rg-data/providers/Microsoft.Compute/disks/disk-%02d", n)
}

func TestAzureDiskSeriesFromItems(t *testing.T) {
	disk1, disk2, disk3 := azureTestDiskID(1), azureTestDiskID(2), azureTestDiskID(3)
	items := []providers.MetricItem{
		{Name: "Composite Disk Read Operations/sec", Statistics: "Average", ResourceId: disk1, Values: []float64{120, 0, 80}, Timestamps: azureTestTimes(0, 1, 2)},
		{Name: "Composite Disk Write Operations/sec", Statistics: "Average", ResourceId: disk1, Values: []float64{30}, Timestamps: azureTestTimes(0)},
		{Name: "Composite Disk Read Bytes/sec", Statistics: "Average", ResourceId: disk1, Values: []float64{1048576}, Timestamps: azureTestTimes(0)},
		{Name: "Composite Disk Write Bytes/sec", Statistics: "Average", ResourceId: disk1, Values: []float64{524288}, Timestamps: azureTestTimes(0)},
		// Azure can hand a resource id back in a different case than it was asked for.
		{Name: "Composite Disk Read Operations/sec", Statistics: "Average", ResourceId: strings.ToUpper(disk2), Values: []float64{5}, Timestamps: azureTestTimes(0)},
		{Name: "Percentage CPU", Statistics: "Average", ResourceId: disk1, Values: []float64{99}, Timestamps: azureTestTimes(0)},
	}

	series := azureDiskSeriesFromItems([]string{disk1, disk2, disk3}, items, testAzureWindowStart, testAzureWindowEnd)
	require.Len(t, series, 3)

	first := series[0]
	assert.Equal(t, disk1, first.DiskID)
	assert.Equal(t, testAzureWindowStart, first.Start)
	assert.Equal(t, testAzureWindowEnd, first.End)
	assert.Equal(t, int64(300), first.PeriodSeconds)
	assert.True(t, first.ValuesAreRates)
	require.NotNil(t, first.ReadOps)
	assert.Equal(t, []float64{120, 0, 80}, first.ReadOps.Values)
	require.NotNil(t, first.WriteOps)
	assert.Equal(t, []float64{30}, first.WriteOps.Values)
	require.NotNil(t, first.ReadBytes)
	assert.Equal(t, []float64{1048576}, first.ReadBytes.Values)
	require.NotNil(t, first.WriteBytes)
	assert.Equal(t, []float64{524288}, first.WriteBytes.Values)
	assert.Nil(t, first.IOPSExceeded)
	assert.Nil(t, first.ThroughputExceeded)

	second := series[1]
	assert.Equal(t, disk2, second.DiskID)
	require.NotNil(t, second.ReadOps)
	assert.Equal(t, []float64{5}, second.ReadOps.Values)
	assert.Nil(t, second.WriteOps)

	third := series[2]
	assert.Equal(t, disk3, third.DiskID)
	assert.True(t, third.ValuesAreRates)
	assert.Nil(t, third.ReadOps)
	assert.Nil(t, third.WriteOps)
	assert.Nil(t, third.ReadBytes)
	assert.Nil(t, third.WriteBytes)
}

func azureTestDisks(count int) []providers.DiskConfig {
	disks := make([]providers.DiskConfig, count)
	for i := range disks {
		disks[i] = providers.DiskConfig{DiskID: azureTestDiskID(i)}
	}
	return disks
}

func TestFetchAzureDiskSeriesRequestsAndBatches(t *testing.T) {
	disks := azureTestDisks(23)
	disks = append(disks, disks[0]) // a repeated disk is fetched once

	var requests []providers.QueryMetricsRequest
	query := func(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		requests = append(requests, filter)
		return providers.QueryMetricsResponse{Items: []providers.MetricItem{
			{Name: "Composite Disk Read Operations/sec", Statistics: "Average", ResourceId: filter.ResourceIds[0], Values: []float64{42}, Timestamps: azureTestTimes(0)},
		}}, nil
	}

	var yielded []providers.DiskPerformanceSeries
	err := fetchAzureDiskSeries(providers.NewCloudProviderContext(context.Background()), providers.Account{}, disks,
		testAzureWindowStart, testAzureWindowEnd, query, func(s providers.DiskPerformanceSeries) { yielded = append(yielded, s) })
	require.NoError(t, err)

	require.Len(t, requests, 3)
	wantDisks := []int{10, 10, 3}
	for i, request := range requests {
		assert.Len(t, request.ResourceIds, wantDisks[i])
		assert.Equal(t, "Microsoft.Compute/disks", request.MetricNamespace)
		assert.Equal(t, []string{
			"Composite Disk Read Operations/sec",
			"Composite Disk Write Operations/sec",
			"Composite Disk Read Bytes/sec",
			"Composite Disk Write Bytes/sec",
		}, request.MetricNames)
		assert.Equal(t, []string{"Average"}, request.Statistics)
		assert.Equal(t, 5*time.Minute, request.Step)
		assert.Equal(t, testAzureWindowStart, *request.StartDate)
		assert.Equal(t, testAzureWindowEnd, *request.EndDate)
	}

	require.Len(t, yielded, 23)
	seen := map[string]bool{}
	withData := 0
	for _, series := range yielded {
		seen[series.DiskID] = true
		assert.True(t, series.ValuesAreRates)
		if series.ReadOps != nil {
			withData++
		}
	}
	assert.Len(t, seen, 23)
	assert.Equal(t, 3, withData)
}

func TestFetchAzureDiskSeriesSkipsFailedBatch(t *testing.T) {
	disks := azureTestDisks(12)
	failure := errors.New("failed to query metrics for all resources")

	calls := 0
	query := func(_ providers.CloudProviderContext, _ providers.Account, _ providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
		calls++
		if calls == 1 {
			return providers.QueryMetricsResponse{}, failure
		}
		return providers.QueryMetricsResponse{}, nil
	}

	var yielded []string
	err := fetchAzureDiskSeries(providers.NewCloudProviderContext(context.Background()), providers.Account{}, disks,
		testAzureWindowStart, testAzureWindowEnd, query, func(s providers.DiskPerformanceSeries) { yielded = append(yielded, s.DiskID) })

	// The first batch of ten failed, so only the last two disks are yielded.
	require.ErrorIs(t, err, failure)
	assert.Equal(t, []string{azureTestDiskID(10), azureTestDiskID(11)}, yielded)
}

// A series carrying a different or missing resource id belongs to some other
// resource and must not be read as this disk's usage.
func TestAzureDiskSeriesFromItemsIgnoresOtherResources(t *testing.T) {
	disk1 := azureTestDiskID(1)
	items := []providers.MetricItem{
		{Name: "Composite Disk Read Operations/sec", ResourceId: azureTestDiskID(2), Values: []float64{900}, Timestamps: azureTestTimes(0)},
		{Name: "Composite Disk Read Operations/sec", ResourceId: "", Values: []float64{800}, Timestamps: azureTestTimes(0)},
		{Name: "Composite Disk Read Operations/sec", ResourceId: testAzureVMID, Values: []float64{700}, Timestamps: azureTestTimes(0)},
	}

	series := azureDiskSeriesFromItems([]string{disk1}, items, testAzureWindowStart, testAzureWindowEnd)

	require.Len(t, series, 1)
	assert.Equal(t, disk1, series[0].DiskID)
	assert.Nil(t, series[0].ReadOps)
}
