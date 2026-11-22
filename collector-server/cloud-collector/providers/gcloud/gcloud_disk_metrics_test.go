package gcloud

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testGcloudDiskA      = testGcloudZoneURL + "/disks/disk-a"
	testGcloudDiskB      = testGcloudZoneURL + "/disks/disk-b"
	testGcloudDiskC      = testGcloudZoneURL + "/disks/disk-c"
	testGcloudInstanceV1 = testGcloudZoneURL + "/instances/vm-1"
	testGcloudInstanceV2 = testGcloudZoneURL + "/instances/vm-2"
)

var (
	testGcloudWindowEnd   = time.Date(2026, 10, 9, 11, 5, 0, 0, time.UTC)
	testGcloudWindowStart = testGcloudWindowEnd.Add(-providers.DiskObservationWindow)
)

func gcloudTestTimes(n ...int) []time.Time {
	times := make([]time.Time, len(n))
	for i, step := range n {
		times[i] = testGcloudWindowStart.Add(time.Duration(step) * providers.DiskMetricPeriod)
	}
	return times
}

func gcloudTestInstance(id uint64, disks ...*computepb.AttachedDisk) *computepb.Instance {
	return &computepb.Instance{Id: proto.Uint64(id), Disks: disks}
}

func gcloudTestAttachedDisk(source, deviceName string) *computepb.AttachedDisk {
	attached := &computepb.AttachedDisk{Source: proto.String(source)}
	if deviceName != "" {
		attached.DeviceName = proto.String(deviceName)
	}
	return attached
}

// gcloudTestMetric is one instance's series for a device, as Cloud Monitoring returns it.
func gcloudTestMetric(name, instanceID, deviceName string, values []float64, times []time.Time) providers.MetricItem {
	return providers.MetricItem{
		Name:       name,
		Statistics: "Delta",
		ResourceId: instanceID,
		Values:     values,
		Timestamps: times,
		Labels:     map[string]string{"device_name": deviceName, "instance_name": "vm", "storage_type": "hyperdisk-balanced"},
	}
}

func gcloudTestAttachment(diskID, name, instanceID, deviceName string) gcloudDiskAttachment {
	return gcloudDiskAttachment{
		Disk:       providers.DiskConfig{DiskID: diskID, DiskName: name, DiskType: "hyperdisk-balanced", Zone: testGcloudZone},
		InstanceID: instanceID,
		DeviceName: deviceName,
	}
}

func TestGcloudDiskDeviceName(t *testing.T) {
	instance := gcloudTestInstance(111,
		gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/boot", "persistent-disk-0"),
		gcloudTestAttachedDisk(testGcloudDiskA, "data-device"),
		gcloudTestAttachedDisk(testGcloudDiskB, ""),
		&computepb.AttachedDisk{DeviceName: proto.String("local-ssd-0")},
	)

	tests := []struct {
		name       string
		diskID     string
		wantDevice string
		wantFound  bool
	}{
		{"device name differs from the disk name", testGcloudDiskA, "data-device", true},
		{"disk id written as a path", "projects/my-project/zones/us-central1-a/disks/disk-a", "data-device", true},
		{"disk id on a different API host", "https://compute.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/disks/disk-a", "data-device", true},
		{"attached without a device name", testGcloudDiskB, "", false},
		{"not attached to this instance", testGcloudDiskC, "", false},
		{"same disk name in another zone", "projects/my-project/zones/us-central1-b/disks/disk-a", "", false},
		{"disk id that is only a name", "disk-a", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device, found := gcloudDiskDeviceName(instance, tt.diskID)
			assert.Equal(t, tt.wantDevice, device)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestGcloudDiskAttachmentFollowsDisk(t *testing.T) {
	// Attached under its own name, as the GKE CSI driver attaches volumes.
	assert.True(t, gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a").followsDisk())
	// A per-instance device name says nothing about which disk it is.
	assert.False(t, gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "persistent-disk-0").followsDisk())
	assert.False(t, gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "data-device").followsDisk())
	assert.False(t, gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "").followsDisk())
	assert.False(t, gcloudTestAttachment(testGcloudDiskA, "", "111", "").followsDisk())
}

func TestGcloudDiskSeriesFilter(t *testing.T) {
	assert.Equal(t,
		`metric.type = "compute.googleapis.com/instance/disk/read_ops_count" AND resource.type = "gce_instance" AND resource.labels.zone = "us-central1-a" AND metric.labels.device_name = one_of("pvc-1", "pvc-2")`,
		gcloudDiskSeriesFilter("disk/read_ops_count", "us-central1-a", []string{"pvc-1", "pvc-2"}, ""))

	assert.Equal(t,
		`metric.type = "compute.googleapis.com/instance/disk/write_bytes_count" AND resource.type = "gce_instance" AND resource.labels.zone = "us-central1-a" AND metric.labels.device_name = one_of("persistent-disk-0") AND resource.labels.instance_id = "111"`,
		gcloudDiskSeriesFilter("disk/write_bytes_count", "us-central1-a", []string{"persistent-disk-0"}, "111"))

	// A quote in a device name cannot break out of the filter.
	assert.Contains(t, gcloudDiskSeriesFilter("disk/read_ops_count", "z", []string{`a"b`}, ""), `one_of("a\"b")`)
}

func TestGcloudDiskMetricItem(t *testing.T) {
	point := func(end time.Time, value *monitoringpb.TypedValue) *monitoringpb.Point {
		return &monitoringpb.Point{Interval: &monitoringpb.TimeInterval{EndTime: timestamppb.New(end)}, Value: value}
	}
	series := &monitoringpb.TimeSeries{
		Metric:   &metricpb.Metric{Type: "compute.googleapis.com/instance/disk/read_ops_count", Labels: map[string]string{"device_name": "pvc-1", "storage_type": "hyperdisk-balanced"}},
		Resource: &monitoredrespb.MonitoredResource{Type: "gce_instance", Labels: map[string]string{"instance_id": "111", "zone": "us-central1-a"}},
		Points: []*monitoringpb.Point{
			point(gcloudTestTimes(2)[0], &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_Int64Value{Int64Value: 900}}),
			point(gcloudTestTimes(1)[0], &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_DoubleValue{DoubleValue: 0}}),
			point(gcloudTestTimes(0)[0], &monitoringpb.TypedValue{Value: &monitoringpb.TypedValue_BoolValue{BoolValue: true}}),
		},
	}

	item := gcloudDiskMetricItem("disk/read_ops_count", series)

	assert.Equal(t, "disk/read_ops_count", item.Name)
	assert.Equal(t, "111", item.ResourceId)
	assert.Equal(t, "pvc-1", item.Labels["device_name"])
	assert.Equal(t, "hyperdisk-balanced", item.Labels["storage_type"])
	// A zero is a measurement and is kept; a value of an unexpected kind is not.
	assert.Equal(t, []float64{900, 0}, item.Values)
	assert.Equal(t, gcloudTestTimes(2, 1), item.Timestamps)
}

// The disk is attached under a per-instance device name, so only the series of the
// instance it is attached to now can be its own.
func TestGcloudDiskHistoryPinnedToCurrentInstance(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "data-device")
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "data-device", []float64{300, 0, 900}, gcloudTestTimes(2, 1, 0)),
		// Another disk on the same instance.
		gcloudTestMetric("disk/read_ops_count", "111", "persistent-disk-0", []float64{99999}, gcloudTestTimes(0)),
		// The same device name on a different instance is a different disk.
		gcloudTestMetric("disk/read_ops_count", "222", "data-device", []float64{88888}, gcloudTestTimes(3)),
		// A different metric of the right device.
		gcloudTestMetric("disk/write_ops_count", "111", "data-device", []float64{77777}, gcloudTestTimes(0)),
		// A series with no device label at all.
		{Name: "disk/read_ops_count", ResourceId: "111", Values: []float64{66666}, Timestamps: gcloudTestTimes(0)},
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	require.True(t, ok)
	require.NotNil(t, history)
	assert.Equal(t, "disk/read_ops_count", history.Name)
	assert.Equal(t, testGcloudDiskA, history.ResourceId)
	assert.Equal(t, gcloudTestTimes(0, 1, 2), history.Timestamps)
	assert.Equal(t, []float64{900, 0, 300}, history.Values)
}

// The disk is attached under its own name and has moved between three instances, as a
// Kubernetes volume does when its node is replaced. Its history is the three series
// joined, with the interval it moved in summed.
func TestGcloudDiskHistoryFollowsDiskAcrossInstances(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "333", "disk-a")
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{10, 20, 30}, gcloudTestTimes(0, 1, 2)),
		// Moved during interval 2: both instances report part of it.
		gcloudTestMetric("disk/read_ops_count", "222", "disk-a", []float64{5, 40, 50}, gcloudTestTimes(2, 3, 4)),
		// Detached for a while, then attached to the current instance.
		gcloudTestMetric("disk/read_ops_count", "333", "disk-a", []float64{60, 70}, gcloudTestTimes(8, 9)),
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	require.True(t, ok)
	require.NotNil(t, history)
	assert.Equal(t, gcloudTestTimes(0, 1, 2, 3, 4, 8, 9), history.Timestamps)
	assert.Equal(t, []float64{10, 20, 35, 40, 50, 60, 70}, history.Values)
}

// A disk that goes back and forth between the same two instances many times shares
// one interval per move. That is still one disk, however many moves there are.
func TestGcloudDiskHistoryAllowsRepeatedMovesBetweenTheSameInstances(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a")
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{1, 1, 1, 1, 1, 1, 1, 1}, gcloudTestTimes(0, 1, 2, 6, 7, 8, 12, 13)),
		gcloudTestMetric("disk/read_ops_count", "222", "disk-a", []float64{1, 1, 1, 1, 1, 1, 1, 1}, gcloudTestTimes(2, 3, 4, 5, 6, 8, 9, 10)),
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	require.True(t, ok)
	require.NotNil(t, history)
	assert.Len(t, history.Timestamps, 13)
}

// Two instances report a device with the disk's name at the same time, interval after
// interval. One disk is on one instance at a time, so these are two devices that
// merely share a name. Their series must not be joined into one history.
func TestGcloudDiskHistoryRefusesUnrelatedDevicesSharingTheName(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a")
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{10, 10, 10, 10, 10}, gcloudTestTimes(0, 1, 2, 3, 4)),
		gcloudTestMetric("disk/read_ops_count", "222", "disk-a", []float64{90000, 90000, 90000}, gcloudTestTimes(1, 2, 3)),
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	assert.False(t, ok)
	assert.Nil(t, history)
}

// A series under the disk's name but with a different storage type cannot be this disk.
func TestGcloudDiskHistoryIgnoresSeriesOfAnotherStorageType(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a")
	other := gcloudTestMetric("disk/read_ops_count", "222", "disk-a", []float64{90000}, gcloudTestTimes(5))
	other.Labels["storage_type"] = "pd-ssd"
	unlabelled := gcloudTestMetric("disk/read_ops_count", "333", "disk-a", []float64{80000}, gcloudTestTimes(6))
	delete(unlabelled.Labels, "storage_type")
	sameTypeOtherCase := gcloudTestMetric("disk/read_ops_count", "444", "disk-a", []float64{7}, gcloudTestTimes(7))
	sameTypeOtherCase.Labels["storage_type"] = "Hyperdisk-Balanced"
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{10}, gcloudTestTimes(0)),
		other, unlabelled, sameTypeOtherCase,
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	require.True(t, ok)
	require.NotNil(t, history)
	assert.Equal(t, gcloudTestTimes(0, 7), history.Timestamps)
	assert.Equal(t, []float64{10, 7}, history.Values)
}

// A disk deleted and created again under the same name is a different disk. Usage from
// before this disk existed belongs to the earlier one.
func TestGcloudDiskHistoryDropsUsageFromBeforeTheDiskWasCreated(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "222", "disk-a")
	attachment.Disk.CreatedAt = gcloudTestTimes(5)[0]
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{90000, 90000, 90000}, gcloudTestTimes(1, 2, 3)),
		gcloudTestMetric("disk/read_ops_count", "222", "disk-a", []float64{1, 2, 3}, gcloudTestTimes(5, 6, 7)),
	}

	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", items)

	require.True(t, ok)
	require.NotNil(t, history)
	// The point stamped at the creation time covers the interval before it.
	assert.Equal(t, gcloudTestTimes(6, 7), history.Timestamps)
	assert.Equal(t, []float64{2, 3}, history.Values)
}

func TestGcloudDiskHistoryNoSeries(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a")
	history, ok := gcloudDiskHistory(attachment, "disk/read_ops_count", nil)
	assert.True(t, ok)
	assert.Nil(t, history)
}

// If one metric's history is refused the disk gets no measurements at all, rather than
// some metrics from one device and some from another.
func TestGcloudDiskSeriesFromItemsRefusedHistoryLeavesNoMeasurements(t *testing.T) {
	attachment := gcloudTestAttachment(testGcloudDiskA, "disk-a", "111", "disk-a")
	items := []providers.MetricItem{
		gcloudTestMetric("disk/read_ops_count", "111", "disk-a", []float64{10, 10, 10, 10}, gcloudTestTimes(0, 1, 2, 3)),
		gcloudTestMetric("disk/write_ops_count", "111", "disk-a", []float64{10, 10, 10, 10}, gcloudTestTimes(0, 1, 2, 3)),
		gcloudTestMetric("disk/read_bytes_count", "111", "disk-a", []float64{10, 10, 10, 10}, gcloudTestTimes(0, 1, 2, 3)),
		gcloudTestMetric("disk/write_bytes_count", "111", "disk-a", []float64{10, 10, 10, 10}, gcloudTestTimes(0, 1, 2, 3)),
		gcloudTestMetric("disk/write_bytes_count", "222", "disk-a", []float64{9, 9, 9, 9}, gcloudTestTimes(0, 1, 2, 3)),
	}

	series, ok := gcloudDiskSeriesFromItems(attachment, items, testGcloudWindowStart, testGcloudWindowEnd)

	assert.False(t, ok)
	assert.Equal(t, testGcloudDiskA, series.DiskID)
	assert.Equal(t, int64(300), series.PeriodSeconds)
	assert.Nil(t, series.ReadOps)
	assert.Nil(t, series.WriteOps)
	assert.Nil(t, series.ReadBytes)
	assert.Nil(t, series.WriteBytes)
}

// gcloudTestQueryCall records one call to the series query.
type gcloudTestQueryCall struct {
	zone       string
	devices    []string
	instanceID string
	metric     string
}

func TestFetchGcloudDiskSeries(t *testing.T) {
	disk := func(name string, attachedTo ...string) providers.DiskConfig {
		return providers.DiskConfig{DiskID: testGcloudZoneURL + "/disks/" + name, DiskName: name, DiskType: "hyperdisk-balanced", Zone: testGcloudZone, AttachedTo: attachedTo}
	}
	disks := []providers.DiskConfig{
		disk("pvc-a", testGcloudInstanceV1),
		disk("pvc-b", testGcloudInstanceV1),
		disk("data", testGcloudInstanceV2),
		disk("unattached"),
		disk("shared", testGcloudInstanceV1, testGcloudInstanceV2),
		disk("detached-since-sync", testGcloudInstanceV2),
	}

	type lookupCall struct{ project, zone, name string }
	var lookups []lookupCall
	getInstance := func(project, zone, name string) (*computepb.Instance, error) {
		lookups = append(lookups, lookupCall{project, zone, name})
		if name == "vm-1" {
			return gcloudTestInstance(111,
				gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/pvc-a", "pvc-a"),
				gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/pvc-b", "pvc-b")), nil
		}
		return gcloudTestInstance(222, gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/data", "persistent-disk-1")), nil
	}

	var calls []gcloudTestQueryCall
	query := func(zone string, deviceNames []string, instanceID, metric string, start, end time.Time) ([]providers.MetricItem, error) {
		calls = append(calls, gcloudTestQueryCall{zone, deviceNames, instanceID, metric})
		assert.Equal(t, testGcloudWindowStart, start)
		assert.Equal(t, testGcloudWindowEnd, end)
		if metric != "disk/read_ops_count" {
			return nil, nil
		}
		if instanceID == "" {
			return []providers.MetricItem{
				// pvc-a was on an earlier instance before the current one.
				gcloudTestMetric(metric, "999", "pvc-a", []float64{7}, gcloudTestTimes(0)),
				gcloudTestMetric(metric, "111", "pvc-a", []float64{10}, gcloudTestTimes(5)),
				gcloudTestMetric(metric, "111", "pvc-b", []float64{20}, gcloudTestTimes(5)),
			}, nil
		}
		return []providers.MetricItem{gcloudTestMetric(metric, "222", "persistent-disk-1", []float64{30}, gcloudTestTimes(5))}, nil
	}

	yielded := map[string]providers.DiskPerformanceSeries{}
	err := fetchGcloudDiskSeries(providers.NewCloudProviderContext(context.Background()), disks,
		testGcloudWindowStart, testGcloudWindowEnd, getInstance, query, func(s providers.DiskPerformanceSeries) {
			_, duplicate := yielded[s.DiskID]
			assert.False(t, duplicate, "disk %s yielded twice", s.DiskID)
			yielded[s.DiskID] = s
		})
	require.NoError(t, err)

	// Each instance is looked up once, however many of its disks are asked for.
	assert.Equal(t, []lookupCall{
		{"my-project", "us-central1-a", "vm-1"},
		{"my-project", "us-central1-a", "vm-2"},
	}, lookups)

	// The two disks attached under their own names are read together across the
	// zone; the one under a per-instance device name is read on its instance only.
	require.Len(t, calls, 8)
	for i, metric := range []string{"disk/read_ops_count", "disk/write_ops_count", "disk/read_bytes_count", "disk/write_bytes_count"} {
		assert.Equal(t, gcloudTestQueryCall{"us-central1-a", []string{"pvc-a", "pvc-b"}, "", metric}, calls[i])
		assert.Equal(t, gcloudTestQueryCall{"us-central1-a", []string{"persistent-disk-1"}, "222", metric}, calls[4+i])
	}

	require.Len(t, yielded, 6)
	pvcA := yielded[testGcloudZoneURL+"/disks/pvc-a"]
	require.NotNil(t, pvcA.ReadOps)
	assert.Equal(t, []float64{7, 10}, pvcA.ReadOps.Values)
	assert.Equal(t, int64(300), pvcA.PeriodSeconds)
	assert.False(t, pvcA.ValuesAreRates)
	require.NotNil(t, yielded[testGcloudZoneURL+"/disks/pvc-b"].ReadOps)
	assert.Equal(t, []float64{20}, yielded[testGcloudZoneURL+"/disks/pvc-b"].ReadOps.Values)
	require.NotNil(t, yielded[testGcloudZoneURL+"/disks/data"].ReadOps)
	assert.Equal(t, []float64{30}, yielded[testGcloudZoneURL+"/disks/data"].ReadOps.Values)

	// Unattached, shared and no-longer-attached disks have no usable history.
	for _, name := range []string{"unattached", "shared", "detached-since-sync"} {
		series, ok := yielded[testGcloudZoneURL+"/disks/"+name]
		require.True(t, ok, "%s was not yielded", name)
		assert.Nil(t, series.ReadOps)
		assert.Nil(t, series.WriteOps)
		assert.Equal(t, int64(300), series.PeriodSeconds)
	}
}

func TestFetchGcloudDiskSeriesBatchesDisksPerZone(t *testing.T) {
	var disks []providers.DiskConfig
	var attached []*computepb.AttachedDisk
	for i := 0; i < 45; i++ {
		name := fmt.Sprintf("pvc-%02d", i)
		disks = append(disks, providers.DiskConfig{DiskID: testGcloudZoneURL + "/disks/" + name, DiskName: name, DiskType: "hyperdisk-balanced", Zone: testGcloudZone, AttachedTo: []string{testGcloudInstanceV1}})
		attached = append(attached, gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/"+name, name))
	}
	getInstance := func(_, _, _ string) (*computepb.Instance, error) { return gcloudTestInstance(111, attached...), nil }

	var batches []int
	query := func(_ string, deviceNames []string, _, metric string, _, _ time.Time) ([]providers.MetricItem, error) {
		if metric == "disk/read_ops_count" {
			batches = append(batches, len(deviceNames))
		}
		return nil, nil
	}

	yielded := 0
	err := fetchGcloudDiskSeries(providers.NewCloudProviderContext(context.Background()), disks,
		testGcloudWindowStart, testGcloudWindowEnd, getInstance, query, func(providers.DiskPerformanceSeries) { yielded++ })

	require.NoError(t, err)
	assert.Equal(t, []int{20, 20, 5}, batches)
	assert.Equal(t, 45, yielded)
}

func TestFetchGcloudDiskSeriesSkipsFailures(t *testing.T) {
	disk := func(name, instance string) providers.DiskConfig {
		return providers.DiskConfig{DiskID: testGcloudZoneURL + "/disks/" + name, DiskName: name, DiskType: "hyperdisk-balanced", Zone: testGcloudZone, AttachedTo: []string{instance}}
	}
	disks := []providers.DiskConfig{
		disk("pvc-a", testGcloudInstanceV1),
		disk("pvc-b", testGcloudInstanceV1),
		disk("data-c", testGcloudInstanceV2),
		disk("data-d", testGcloudZoneURL+"/instances/vm-3"),
	}
	lookupFailure := errors.New("googleapi: Error 503: backend unavailable")
	queryFailure := errors.New("rpc error: code = Unavailable")

	lookups := 0
	getInstance := func(_, _, name string) (*computepb.Instance, error) {
		lookups++
		switch name {
		case "vm-1":
			return nil, lookupFailure
		case "vm-2":
			return gcloudTestInstance(222, gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/data-c", "device-c")), nil
		default:
			return gcloudTestInstance(333, gcloudTestAttachedDisk(testGcloudZoneURL+"/disks/data-d", "device-d")), nil
		}
	}
	query := func(_ string, _ []string, instanceID, metric string, _, _ time.Time) ([]providers.MetricItem, error) {
		if instanceID == "222" && metric == "disk/read_bytes_count" {
			return nil, queryFailure
		}
		return []providers.MetricItem{gcloudTestMetric(metric, instanceID, "device-d", []float64{40}, gcloudTestTimes(0))}, nil
	}

	var yielded []providers.DiskPerformanceSeries
	err := fetchGcloudDiskSeries(providers.NewCloudProviderContext(context.Background()), disks,
		testGcloudWindowStart, testGcloudWindowEnd, getInstance, query, func(s providers.DiskPerformanceSeries) { yielded = append(yielded, s) })

	// Both failures are reported, the failed instance is not looked up again for its
	// second disk, and the affected disks are not yielded as if they had no usage —
	// including the disk for which only one of the four metrics failed.
	require.ErrorIs(t, err, lookupFailure)
	require.ErrorIs(t, err, queryFailure)
	assert.Equal(t, 3, lookups)
	require.Len(t, yielded, 1)
	assert.Equal(t, testGcloudZoneURL+"/disks/data-d", yielded[0].DiskID)
	require.NotNil(t, yielded[0].WriteOps)
	assert.Equal(t, []float64{40}, yielded[0].WriteOps.Values)
}
