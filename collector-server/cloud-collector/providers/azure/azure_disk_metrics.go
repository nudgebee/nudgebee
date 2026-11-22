package azure

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/samber/lo"
)

const (
	azureDiskMetricsNamespace = "Microsoft.Compute/disks"

	// azureDiskMetricsBatch is how many disks are fetched and handed on at a time.
	// Azure Monitor is queried one disk per request either way; the batch only bounds
	// how much history is held in memory at once.
	azureDiskMetricsBatch = 10

	// Metric names as the Azure Monitor REST API spells them for
	// Microsoft.Compute/disks. All four are per-second rates and support only the
	// Average aggregation.
	azureDiskReadOpsMetric    = "Composite Disk Read Operations/sec"
	azureDiskWriteOpsMetric   = "Composite Disk Write Operations/sec"
	azureDiskReadBytesMetric  = "Composite Disk Read Bytes/sec"
	azureDiskWriteBytesMetric = "Composite Disk Write Bytes/sec"
)

var azureDiskUsageMetrics = []string{azureDiskReadOpsMetric, azureDiskWriteOpsMetric, azureDiskReadBytesMetric, azureDiskWriteBytesMetric}

// azureDiskSeriesFromItems turns the items Azure Monitor returned into one series per
// requested disk, in the order requested. A disk Azure Monitor returned nothing for
// still gets a series, with every metric nil.
func azureDiskSeriesFromItems(diskIDs []string, items []providers.MetricItem, start, end time.Time) []providers.DiskPerformanceSeries {
	itemsByDiskAndMetric := lo.GroupBy(items, func(item providers.MetricItem) string {
		return strings.ToLower(item.ResourceId) + "|" + item.Name
	})
	metric := func(diskID, name string) *providers.MetricItem {
		return providers.MergeMetricItems(itemsByDiskAndMetric[strings.ToLower(diskID)+"|"+name])
	}

	series := make([]providers.DiskPerformanceSeries, 0, len(diskIDs))
	for _, diskID := range diskIDs {
		series = append(series, providers.DiskPerformanceSeries{
			DiskID:         diskID,
			Start:          start,
			End:            end,
			PeriodSeconds:  int64(providers.DiskMetricPeriod.Seconds()),
			ValuesAreRates: true,
			ReadOps:        metric(diskID, azureDiskReadOpsMetric),
			WriteOps:       metric(diskID, azureDiskWriteOpsMetric),
			ReadBytes:      metric(diskID, azureDiskReadBytesMetric),
			WriteBytes:     metric(diskID, azureDiskWriteBytesMetric),
		})
	}
	return series
}

// fetchAzureDiskSeries reads the usage history of the given managed disks from Azure
// Monitor and passes each disk's series to yield. Disks are fetched a batch at a time
// and each batch is released once yielded, so the caller should reduce a series
// before returning from yield rather than keep it.
//
// The metric names are passed explicitly: Microsoft.Compute/disks has no default
// metric list, so the scheduled metrics collection does not pick these up.
//
// A batch in which every disk fails is skipped and reported in the returned error.
// Within a batch, the query returns what it could and a disk that failed comes back
// with no measurements. Throttling and transient errors are retried by the Azure SDK.
func fetchAzureDiskSeries(ctx providers.CloudProviderContext, account providers.Account, disks []providers.DiskConfig, start, end time.Time, query providers.DiskMetricsQuery, yield func(providers.DiskPerformanceSeries)) error {
	diskIDs := lo.Uniq(lo.Map(disks, func(disk providers.DiskConfig, _ int) string { return disk.DiskID }))

	var errs []error
	for _, batch := range lo.Chunk(diskIDs, azureDiskMetricsBatch) {
		resp, err := query(ctx, account, providers.QueryMetricsRequest{
			ResourceIds:     batch,
			ServiceName:     azureDiskMetricsNamespace,
			MetricNamespace: azureDiskMetricsNamespace,
			MetricNames:     azureDiskUsageMetrics,
			Statistics:      []string{"Average"},
			Step:            providers.DiskMetricPeriod,
			StartDate:       &start,
			EndDate:         &end,
		})
		if err != nil {
			ctx.GetLogger().Error("failed to fetch azure disk metrics", "error", err, "accountNumber", account.AccountNumber, "disks", len(batch))
			errs = append(errs, fmt.Errorf("azure disk metrics (%d disks): %w", len(batch), err))
			continue
		}
		for _, series := range azureDiskSeriesFromItems(batch, resp.Items, start, end) {
			yield(series)
		}
	}
	return errors.Join(errs...)
}
