package aws

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/samber/lo"
)

const (
	ebsMetricsNamespace = "AWS/EBS"

	// ebsMetricsVolumeBatch is how many volumes are fetched and handed on at a time.
	// A volume's 30-day history is about 34,600 datapoints (four metrics at five
	// minutes), so the batch bounds how much of it is held in memory at once. It also
	// keeps a batch of singly attached volumes, at six queries each, under
	// GetMetricData's 500-query limit; getAwsCloudwatchMetrics splits anything larger.
	ebsMetricsVolumeBatch = 25

	// ebsExceededCheckPeriodSeconds is the period of the exceeded-check flags. They
	// are 0 or 1 per minute, so the daily maximum says whether the limit was hit at
	// all that day, for 30 points per volume instead of thousands.
	ebsExceededCheckPeriodSeconds = 24 * 60 * 60

	ebsReadOpsMetric            = "VolumeReadOps"
	ebsWriteOpsMetric           = "VolumeWriteOps"
	ebsReadBytesMetric          = "VolumeReadBytes"
	ebsWriteBytesMetric         = "VolumeWriteBytes"
	ebsIOPSExceededMetric       = "VolumeIOPSExceededCheck"
	ebsThroughputExceededMetric = "VolumeThroughputExceededCheck"

	// ebsMetricLabelSeparator joins a volume id and a metric name in a query label.
	// Neither can contain it.
	ebsMetricLabelSeparator = "|"
)

// ebsUsageSampleSeconds is how long each sample behind the usage metrics covers. EBS
// publishes them once a minute, so the Maximum statistic of a five-minute interval is
// the total of its busiest minute. Reading that, rather than the interval's Sum,
// keeps a burst of a minute or two from being averaged away over five, at the same
// number of datapoints; CloudWatch keeps each interval's maximum for as long as it
// keeps the interval. Checked against real volumes: every five-minute Maximum
// equalled the largest of its one-minute Sums.
//
// It is still a one-minute figure, while EBS enforces IOPS and throughput per
// second, so a burst of a few seconds inside a quieter minute is understated. The
// headroom added above the peak and the utilization gate leave a margin for that;
// they are not a guarantee.
//
// AWS documents Minimum and Maximum on these metrics as supported only for volumes
// attached to Nitro-based instances. A volume without them returns nothing and is
// reported as not evaluated rather than judged on five-minute averages.
const ebsUsageSampleSeconds = 60

var ebsUsageMetrics = []string{ebsReadOpsMetric, ebsWriteOpsMetric, ebsReadBytesMetric, ebsWriteBytesMetric}

var ebsExceededCheckMetrics = []string{ebsIOPSExceededMetric, ebsThroughputExceededMetric}

// ebsDiskMetricQueries builds the CloudWatch queries for the usage history of the
// given volumes. The per-service CloudWatch defaults resolve AmazonEC2 to the AWS/EC2
// namespace keyed by InstanceId, which has no per-volume data, so each query names
// AWS/EBS and its dimensions itself. A query result carries no resource id on this
// path, so the volume and metric travel in the label.
//
// The usage metrics are published per volume. The exceeded-check flags are published
// per attachment, under both VolumeId and InstanceId, and a query by VolumeId alone
// returns nothing for them; so they are asked for once per instance the volume is
// attached to, and not at all for a volume with no known attachment.
func ebsDiskMetricQueries(disks []providers.DiskConfig) []providers.CloudWatchMetricEntry {
	queries := make([]providers.CloudWatchMetricEntry, 0, len(disks)*(len(ebsUsageMetrics)+len(ebsExceededCheckMetrics)))
	add := func(volumeID, metric, stat string, periodSeconds int, dimensions map[string]string) {
		queries = append(queries, providers.CloudWatchMetricEntry{
			Id:         fmt.Sprintf("q%d", len(queries)),
			ReturnData: true,
			Label:      volumeID + ebsMetricLabelSeparator + metric,
			MetricStat: &providers.CloudWatchMetricEntryStat{
				Stat:   stat,
				Period: periodSeconds,
				Metric: providers.CloudWatchMetricEntryRef{
					Namespace:  ebsMetricsNamespace,
					Name:       metric,
					Dimensions: dimensions,
				},
			},
		})
	}
	for _, disk := range disks {
		for _, metric := range ebsUsageMetrics {
			add(disk.DiskID, metric, "Maximum", int(providers.DiskMetricPeriod.Seconds()), map[string]string{"VolumeId": disk.DiskID})
		}
		for _, instanceID := range disk.AttachedTo {
			for _, metric := range ebsExceededCheckMetrics {
				add(disk.DiskID, metric, "Maximum", ebsExceededCheckPeriodSeconds, map[string]string{"VolumeId": disk.DiskID, "InstanceId": instanceID})
			}
		}
	}
	return queries
}

// ebsFlagSeries joins the exceeded-check results of one volume into a single series,
// keeping the highest value at each timestamp, so a flag raised on any one of a
// multi-attached volume's instances is not lost.
func ebsFlagSeries(items []providers.MetricItem) *providers.MetricItem {
	highest := map[time.Time]float64{}
	for _, item := range items {
		for i := 0; i < len(item.Values) && i < len(item.Timestamps); i++ {
			timestamp := item.Timestamps[i].UTC()
			if value, seen := highest[timestamp]; !seen || item.Values[i] > value {
				highest[timestamp] = item.Values[i]
			}
		}
	}
	if len(highest) == 0 {
		return nil
	}
	merged := items[0]
	merged.Timestamps = slices.SortedFunc(maps.Keys(highest), func(a, b time.Time) int { return a.Compare(b) })
	merged.Values = make([]float64, len(merged.Timestamps))
	for i, timestamp := range merged.Timestamps {
		merged.Values[i] = highest[timestamp]
	}
	return &merged
}

// ebsDiskSeriesFromItems turns the items returned for ebsDiskMetricQueries into one
// series per requested volume, in the order requested. A volume CloudWatch returned
// nothing for still gets a series, with every metric nil.
func ebsDiskSeriesFromItems(volumeIDs []string, items []providers.MetricItem, start, end time.Time) []providers.DiskPerformanceSeries {
	itemsByLabel := lo.GroupBy(items, func(item providers.MetricItem) string { return item.Name })

	named := func(merged *providers.MetricItem, volumeID, name string) *providers.MetricItem {
		if merged != nil {
			merged.Name = name
			merged.ResourceId = volumeID
		}
		return merged
	}
	metric := func(volumeID, name string) *providers.MetricItem {
		return named(providers.MergeMetricItems(itemsByLabel[volumeID+ebsMetricLabelSeparator+name]), volumeID, name)
	}
	flag := func(volumeID, name string) *providers.MetricItem {
		return named(ebsFlagSeries(itemsByLabel[volumeID+ebsMetricLabelSeparator+name]), volumeID, name)
	}

	series := make([]providers.DiskPerformanceSeries, 0, len(volumeIDs))
	for _, volumeID := range volumeIDs {
		series = append(series, providers.DiskPerformanceSeries{
			DiskID:             volumeID,
			Start:              start,
			End:                end,
			PeriodSeconds:      int64(providers.DiskMetricPeriod.Seconds()),
			SampleSeconds:      ebsUsageSampleSeconds,
			ReadOps:            metric(volumeID, ebsReadOpsMetric),
			WriteOps:           metric(volumeID, ebsWriteOpsMetric),
			ReadBytes:          metric(volumeID, ebsReadBytesMetric),
			WriteBytes:         metric(volumeID, ebsWriteBytesMetric),
			IOPSExceeded:       flag(volumeID, ebsIOPSExceededMetric),
			ThroughputExceeded: flag(volumeID, ebsThroughputExceededMetric),
		})
	}
	return series
}

// fetchEbsDiskSeries reads the usage history of the given volumes from CloudWatch and
// passes each volume's series to yield. Volumes are fetched a region and a batch at a
// time, and each batch is released once yielded, so the caller should reduce a series
// before returning from yield rather than keep it.
//
// A batch that fails is skipped: its volumes are never yielded, the remaining batches
// still run, and the failures come back joined in the returned error. Throttling and
// transient errors are retried by the AWS SDK before a batch counts as failed.
func fetchEbsDiskSeries(ctx providers.CloudProviderContext, account providers.Account, disks []providers.DiskConfig, start, end time.Time, query providers.DiskMetricsQuery, yield func(providers.DiskPerformanceSeries)) error {
	disksByRegion := map[string][]providers.DiskConfig{}
	var regions []string
	seen := map[string]bool{}
	for _, disk := range disks {
		if seen[disk.DiskID] {
			continue
		}
		seen[disk.DiskID] = true
		if _, ok := disksByRegion[disk.Region]; !ok {
			regions = append(regions, disk.Region)
		}
		disksByRegion[disk.Region] = append(disksByRegion[disk.Region], disk)
	}

	var errs []error
	for _, region := range regions {
		for _, batch := range lo.Chunk(disksByRegion[region], ebsMetricsVolumeBatch) {
			volumeIDs := lo.Map(batch, func(disk providers.DiskConfig, _ int) string { return disk.DiskID })
			resp, err := query(ctx, account, providers.QueryMetricsRequest{
				Region:        region,
				StartDate:     &start,
				EndDate:       &end,
				MetricQueries: ebsDiskMetricQueries(batch),
			})
			if err != nil {
				ctx.GetLogger().Error("failed to fetch ebs volume metrics", "error", err, "accountNumber", account.AccountNumber, "region", region, "volumes", len(batch))
				errs = append(errs, fmt.Errorf("ebs volume metrics in %s (%s): %w", region, strings.Join(volumeIDs, ","), err))
				continue
			}
			for _, series := range ebsDiskSeriesFromItems(volumeIDs, resp.Items, start, end) {
				yield(series)
			}
		}
	}
	return errors.Join(errs...)
}
