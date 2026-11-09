package gcloud

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"nudgebee/collector/cloud/providers"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// Disk usage metrics, relative to compute.googleapis.com/instance. All four are
	// DELTA counts sampled every 60 seconds on the gce_instance resource, with the
	// disk identified by the device_name metric label.
	gcloudDiskReadOpsMetric    = "disk/read_ops_count"
	gcloudDiskWriteOpsMetric   = "disk/write_ops_count"
	gcloudDiskReadBytesMetric  = "disk/read_bytes_count"
	gcloudDiskWriteBytesMetric = "disk/write_bytes_count"

	gcloudInstanceMetricPrefix = "compute.googleapis.com/instance/"
	gcloudDiskDeviceNameLabel  = "device_name"
	gcloudDiskStorageTypeLabel = "storage_type"

	// gcloudDiskMetricsDeviceBatch is how many disks of one zone are read per query.
	gcloudDiskMetricsDeviceBatch = 20

	// gcloudMaxHandoffOverlap is how many intervals in a row two instances may both
	// report a disk in when it moves from one to the other: the interval it moved in,
	// and one more for alignment. A longer run means the series are not one disk
	// moving between instances.
	gcloudMaxHandoffOverlap = 2
)

var gcloudDiskUsageMetrics = []string{gcloudDiskReadOpsMetric, gcloudDiskWriteOpsMetric, gcloudDiskReadBytesMetric, gcloudDiskWriteBytesMetric}

// gcloudInstanceLookup fetches one Compute Engine instance.
type gcloudInstanceLookup func(project, zone, name string) (*computepb.Instance, error)

// gcloudDiskSeriesQuery reads one usage metric, totalled per five-minute interval,
// for the series in a zone that carry one of the given device names. With an
// instance id it reads that instance only; without, every instance in the zone. Each
// returned item is one instance's series: ResourceId is the instance id and Labels
// the metric's own labels.
type gcloudDiskSeriesQuery func(zone string, deviceNames []string, instanceID, metric string, start, end time.Time) ([]providers.MetricItem, error)

// gcloudDiskAttachment is where a disk's usage is reported. Cloud Monitoring has no
// per-disk resource: usage is a series on the instance, labelled with the device name
// the disk is attached under.
type gcloudDiskAttachment struct {
	Disk       providers.DiskConfig
	InstanceID string
	DeviceName string
}

// followsDisk reports whether the device name identifies the disk itself rather than
// a slot on one instance. A device name is chosen per attachment, and names like
// persistent-disk-0 repeat on every instance, so a device name alone never
// identifies a disk. But a disk attached under its own name — as the GKE CSI driver
// attaches every volume — carries the disk's name, which is unique in its project and
// zone, onto whichever instance it is attached to. Only then can the disk's history
// be followed across the instances it has been attached to.
func (a gcloudDiskAttachment) followsDisk() bool {
	return a.DeviceName != "" && a.DeviceName == a.Disk.DiskName
}

// newGcloudInstanceLookup returns a lookup backed by the Compute Engine API and a
// function that releases its client.
func newGcloudInstanceLookup(ctx providers.CloudProviderContext, account providers.Account) (gcloudInstanceLookup, func(), error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}
	client, err := compute.NewInstancesRESTClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create instances client: %w", err)
	}
	lookup := func(project, zone, name string) (*computepb.Instance, error) {
		return client.Get(ctx.GetContext(), &computepb.GetInstanceRequest{Project: project, Zone: zone, Instance: name})
	}
	closeClient := func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close instances client", "error", cerr)
		}
	}
	return lookup, closeClient, nil
}

// gcloudResourcePath strips the API host and version from a Compute Engine resource
// URL, leaving projects/<project>/..., so the same resource compares equal whichever
// form it was written in.
func gcloudResourcePath(resourceURL string) string {
	if i := strings.Index(resourceURL, "projects/"); i >= 0 {
		return resourceURL[i:]
	}
	return resourceURL
}

// gcloudDiskDeviceName returns the device name a disk is attached to an instance
// under. The device name is chosen at attach time and need not match the disk's name,
// so it has to be read from the instance.
func gcloudDiskDeviceName(instance *computepb.Instance, diskID string) (string, bool) {
	for _, attached := range instance.GetDisks() {
		if attached.GetSource() != "" && gcloudResourcePath(attached.GetSource()) == gcloudResourcePath(diskID) {
			return attached.GetDeviceName(), attached.GetDeviceName() != ""
		}
	}
	return "", false
}

// gcloudDiskSeriesFilter builds the Cloud Monitoring filter for one usage metric of
// the given device names in a zone, optionally on one instance.
func gcloudDiskSeriesFilter(metric, zone string, deviceNames []string, instanceID string) string {
	quoted := lo.Map(deviceNames, func(name string, _ int) string {
		return `"` + strings.ReplaceAll(name, `"`, `\"`) + `"`
	})
	filter := fmt.Sprintf(`metric.type = "%s%s" AND resource.type = "gce_instance" AND resource.labels.zone = "%s" AND metric.labels.%s = one_of(%s)`,
		gcloudInstanceMetricPrefix, metric, zone, gcloudDiskDeviceNameLabel, strings.Join(quoted, ", "))
	if instanceID != "" {
		filter += fmt.Sprintf(` AND resource.labels.instance_id = "%s"`, instanceID)
	}
	return filter
}

// newGcloudDiskSeriesQuery returns a query backed by Cloud Monitoring and a function
// that releases its client. It does not go through getGcloudMonitoringMetrics, which
// can select series by instance but not by device name, and keeps only a capped
// number of series per metric.
func newGcloudDiskSeriesQuery(ctx providers.CloudProviderContext, account providers.Account) (gcloudDiskSeriesQuery, func(), error) {
	session, err := getGcloudSessionFromAccount(ctx, account)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gcloud session: %w", err)
	}
	client, err := monitoring.NewMetricClient(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create monitoring client: %w", err)
	}
	query := gcloudDiskSeriesQueryFor(ctx, client, session.ProjectId)
	closeClient := func() {
		if cerr := client.Close(); cerr != nil {
			ctx.GetLogger().Error("failed to close monitoring client", "error", cerr)
		}
	}
	return query, closeClient, nil
}

// gcloudDiskSeriesQueryFor returns the series query for a project over an existing
// Cloud Monitoring client.
func gcloudDiskSeriesQueryFor(ctx providers.CloudProviderContext, client *monitoring.MetricClient, project string) gcloudDiskSeriesQuery {
	return func(zone string, deviceNames []string, instanceID, metric string, start, end time.Time) ([]providers.MetricItem, error) {
		it := client.ListTimeSeries(ctx.GetContext(), &monitoringpb.ListTimeSeriesRequest{
			Name:   "projects/" + project,
			Filter: gcloudDiskSeriesFilter(metric, zone, deviceNames, instanceID),
			Interval: &monitoringpb.TimeInterval{
				StartTime: timestamppb.New(start),
				EndTime:   timestamppb.New(end),
			},
			// The metrics are DELTA counts; ALIGN_DELTA totals them per interval.
			Aggregation: &monitoringpb.Aggregation{
				AlignmentPeriod:  durationpb.New(providers.DiskMetricPeriod),
				PerSeriesAligner: monitoringpb.Aggregation_ALIGN_DELTA,
			},
		})
		var items []providers.MetricItem
		for {
			series, err := it.Next()
			if err == iterator.Done {
				return items, nil
			}
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				return nil, fmt.Errorf("failed to read %s: %w", metric, err)
			}
			items = append(items, gcloudDiskMetricItem(metric, series))
		}
	}
}

// gcloudDiskMetricItem converts one Cloud Monitoring time series to a metric item.
func gcloudDiskMetricItem(metric string, series *monitoringpb.TimeSeries) providers.MetricItem {
	item := providers.MetricItem{
		Name:       metric,
		Statistics: "Delta",
		ResourceId: series.GetResource().GetLabels()["instance_id"],
		Labels:     map[string]string{},
	}
	for key, value := range series.GetMetric().GetLabels() {
		item.Labels[key] = value
	}
	for _, point := range series.GetPoints() {
		var value float64
		switch v := point.GetValue().GetValue().(type) {
		case *monitoringpb.TypedValue_Int64Value:
			value = float64(v.Int64Value)
		case *monitoringpb.TypedValue_DoubleValue:
			value = v.DoubleValue
		default:
			continue
		}
		item.Values = append(item.Values, value)
		item.Timestamps = append(item.Timestamps, point.GetInterval().GetEndTime().AsTime())
	}
	return item
}

// gcloudDiskHistory joins the series of one metric that belong to a disk into a single
// series. ok is false when the series cannot all be one disk, in which case nothing
// may be concluded about the disk.
//
// A series is the disk's only if it carries the attachment's device name and the
// disk's own storage type. For a disk attached under a per-instance device name, only
// the series of the instance it is attached to now is taken. For a disk attached
// under its own name, the series of every instance it has been attached to are
// taken, and then checked: points from before the disk was created are dropped, since
// they belong to an earlier disk that had the name; and two instances may report the
// disk in the same interval only around the moment it moves between them. A single
// disk is on one instance at a time, so anything more is two different devices that
// happen to share a name, and the history is refused rather than merged.
func gcloudDiskHistory(attachment gcloudDiskAttachment, metric string, items []providers.MetricItem) (history *providers.MetricItem, ok bool) {
	totals := map[time.Time]float64{}
	instancesAt := map[time.Time]int{}
	for _, item := range items {
		if item.Name != metric || item.Labels[gcloudDiskDeviceNameLabel] != attachment.DeviceName {
			continue
		}
		if !strings.EqualFold(item.Labels[gcloudDiskStorageTypeLabel], attachment.Disk.DiskType) {
			continue
		}
		if !attachment.followsDisk() && item.ResourceId != attachment.InstanceID {
			continue
		}
		for i := 0; i < len(item.Values) && i < len(item.Timestamps); i++ {
			timestamp := item.Timestamps[i].UTC()
			if !attachment.Disk.CreatedAt.IsZero() && !timestamp.After(attachment.Disk.CreatedAt) {
				continue
			}
			totals[timestamp] += item.Values[i]
			instancesAt[timestamp]++
		}
	}
	if len(totals) == 0 {
		return nil, true
	}

	timestamps := make([]time.Time, 0, len(totals))
	for timestamp := range totals {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i].Before(timestamps[j]) })

	// Find the longest run of consecutive intervals reported by more than one instance.
	run := 0
	for i, timestamp := range timestamps {
		if instancesAt[timestamp] < 2 {
			run = 0
			continue
		}
		if i > 0 && instancesAt[timestamps[i-1]] > 1 && timestamp.Sub(timestamps[i-1]) <= providers.DiskMetricPeriod {
			run++
		} else {
			run = 1
		}
		if run > gcloudMaxHandoffOverlap {
			return nil, false
		}
	}
	history = &providers.MetricItem{Name: metric, Statistics: "Delta", ResourceId: attachment.Disk.DiskID, Timestamps: timestamps, Values: make([]float64, len(timestamps))}
	for i, timestamp := range timestamps {
		history.Values[i] = totals[timestamp]
	}
	return history, true
}

// gcloudDiskSeriesFromItems builds a disk's series from the items read for it. If any
// metric's history is refused (see gcloudDiskHistory) the disk gets no measurements
// at all, rather than some metrics from one device and some from another.
func gcloudDiskSeriesFromItems(attachment gcloudDiskAttachment, items []providers.MetricItem, start, end time.Time) (series providers.DiskPerformanceSeries, ok bool) {
	series = emptyGcloudDiskSeries(attachment.Disk.DiskID, start, end)
	targets := map[string]**providers.MetricItem{
		gcloudDiskReadOpsMetric:    &series.ReadOps,
		gcloudDiskWriteOpsMetric:   &series.WriteOps,
		gcloudDiskReadBytesMetric:  &series.ReadBytes,
		gcloudDiskWriteBytesMetric: &series.WriteBytes,
	}
	for _, metric := range gcloudDiskUsageMetrics {
		history, ok := gcloudDiskHistory(attachment, metric, items)
		if !ok {
			return emptyGcloudDiskSeries(attachment.Disk.DiskID, start, end), false
		}
		*targets[metric] = history
	}
	return series, true
}

// emptyGcloudDiskSeries is the series of a disk with no measurements.
func emptyGcloudDiskSeries(diskID string, start, end time.Time) providers.DiskPerformanceSeries {
	return providers.DiskPerformanceSeries{
		DiskID:        diskID,
		Start:         start,
		End:           end,
		PeriodSeconds: int64(providers.DiskMetricPeriod.Seconds()),
	}
}

// fetchGcloudDiskSeries reads the usage history of the given disks from Cloud
// Monitoring and passes each disk's series to yield. The caller should reduce a
// series before returning from yield rather than keep it.
//
// Each attached instance is looked up once, to learn its numeric id and the device
// name the disk is attached under. A disk attached under its own name is then read
// across every instance in its zone, so its history survives the instance being
// replaced — which is routine for the nodes of a Kubernetes cluster. Any other disk
// is read on its current instance only. See gcloudDiskHistory for how series are
// accepted as one disk's.
//
// Only a disk attached to exactly one instance is read. A disk that is unattached,
// attached to several instances, no longer attached where it was, or whose history
// was refused, is yielded with no measurements rather than a partial picture.
//
// A disk whose instance cannot be looked up, or whose query fails, is never yielded;
// the rest still run and the failures come back joined in the returned error.
func fetchGcloudDiskSeries(ctx providers.CloudProviderContext, disks []providers.DiskConfig, start, end time.Time, getInstance gcloudInstanceLookup, query gcloudDiskSeriesQuery, yield func(providers.DiskPerformanceSeries)) error {
	var errs []error
	var following, pinned []gcloudDiskAttachment
	instances := map[string]*computepb.Instance{}

	for _, disk := range disks {
		if len(disk.AttachedTo) != 1 || disk.Zone == "" {
			yield(emptyGcloudDiskSeries(disk.DiskID, start, end))
			continue
		}
		instanceURL := disk.AttachedTo[0]
		instance, ok := instances[instanceURL]
		if !ok {
			project, zone, name := parseInstanceInfoFromResourceId(instanceURL)
			var err error
			instance, err = getInstance(project, zone, name)
			if err != nil {
				RecordGCPPermissionError(ctx, err)
				ctx.GetLogger().Warn("failed to look up instance for disk metrics", "error", err, "instance", instanceURL)
				errs = append(errs, fmt.Errorf("instance %s: %w", instanceURL, err))
			}
			instances[instanceURL] = instance
		}
		if instance == nil {
			continue
		}
		deviceName, found := gcloudDiskDeviceName(instance, disk.DiskID)
		if !found || instance.Id == nil {
			yield(emptyGcloudDiskSeries(disk.DiskID, start, end))
			continue
		}
		attachment := gcloudDiskAttachment{Disk: disk, InstanceID: fmt.Sprintf("%d", instance.GetId()), DeviceName: deviceName}
		if attachment.followsDisk() {
			following = append(following, attachment)
		} else {
			pinned = append(pinned, attachment)
		}
	}

	read := func(attachments []gcloudDiskAttachment, zone, instanceID string) {
		deviceNames := lo.Map(attachments, func(a gcloudDiskAttachment, _ int) string { return a.DeviceName })
		var items []providers.MetricItem
		for _, metric := range gcloudDiskUsageMetrics {
			got, err := query(zone, deviceNames, instanceID, metric, start, end)
			if err != nil {
				ctx.GetLogger().Error("failed to fetch gcp disk metrics", "error", err, "zone", zone, "disks", len(attachments))
				errs = append(errs, fmt.Errorf("gcp disk metrics in %s (%d disks): %w", zone, len(attachments), err))
				return
			}
			items = append(items, got...)
		}
		for _, attachment := range attachments {
			series, ok := gcloudDiskSeriesFromItems(attachment, items, start, end)
			if !ok {
				ctx.GetLogger().Warn("gcp disk metrics: series sharing the disk's name overlap on several instances, history not used",
					"disk", attachment.Disk.DiskID, "device", attachment.DeviceName)
			}
			yield(series)
		}
	}

	byZone := lo.GroupBy(following, func(a gcloudDiskAttachment) string { return a.Disk.Zone })
	zones := lo.Keys(byZone)
	sort.Strings(zones)
	for _, zone := range zones {
		for _, batch := range lo.Chunk(byZone[zone], gcloudDiskMetricsDeviceBatch) {
			read(batch, zone, "")
		}
	}
	for _, attachment := range pinned {
		read([]gcloudDiskAttachment{attachment}, attachment.Disk.Zone, attachment.InstanceID)
	}
	return errors.Join(errs...)
}
