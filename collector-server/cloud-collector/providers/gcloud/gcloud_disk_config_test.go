package gcloud

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const (
	testGcloudProject  = "my-project"
	testGcloudZone     = "us-central1-a"
	testGcloudZoneURL  = "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a"
	testGcloudDiskURL  = testGcloudZoneURL + "/disks/data-disk-1"
	testGcloudInstance = testGcloudZoneURL + "/instances/vm-1"
)

// gcloudDiskResource builds the resource the collector stores for a disk, with Meta
// produced the same way diskService.GetResources produces it: the protobuf struct
// through structToMap, plus the sizeGb and diskType keys the collector adds.
func gcloudDiskResource(disk *computepb.Disk, diskType string) providers.Resource {
	disk.Name = proto.String("data-disk-1")
	disk.SelfLink = proto.String(testGcloudDiskURL)
	disk.Zone = proto.String(testGcloudZoneURL)
	if diskType != "" {
		disk.Type = proto.String(testGcloudZoneURL + "/diskTypes/" + diskType)
	}

	meta := structToMap(disk)
	if disk.SizeGb != nil {
		meta["sizeGb"] = *disk.SizeGb
	}
	if diskType != "" {
		meta["diskType"] = diskType
	}
	return providers.Resource{
		Id:          testGcloudDiskURL,
		Name:        "data-disk-1",
		Type:        "storage",
		ServiceName: ServiceNameDisk,
		Region:      "us-central1",
		Arn:         testGcloudDiskURL,
		Meta:        meta,
	}
}

func TestGcloudDiskConfigFromResource(t *testing.T) {
	tests := []struct {
		name           string
		diskType       string
		iops           *int64
		throughput     *int64
		wantIOPS       *int64
		wantThroughput *float64
		wantIOPSSource providers.DiskPerformanceSource
		wantTputSource providers.DiskPerformanceSource
		wantAdjustable bool
	}{
		{
			name:           "Hyperdisk Balanced has explicit IOPS and throughput",
			diskType:       "hyperdisk-balanced",
			iops:           proto.Int64(6000),
			throughput:     proto.Int64(290),
			wantIOPS:       proto.Int64(6000),
			wantThroughput: proto.Float64(290),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "Hyperdisk Extreme has explicit IOPS and derived throughput",
			diskType:       "hyperdisk-extreme",
			iops:           proto.Int64(100000),
			wantIOPS:       proto.Int64(100000),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceDerived,
			wantAdjustable: true,
		},
		{
			name:           "Hyperdisk Throughput has explicit throughput and derived IOPS",
			diskType:       "hyperdisk-throughput",
			throughput:     proto.Int64(600),
			wantThroughput: proto.Float64(600),
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "pd-extreme has explicit IOPS and derived throughput",
			diskType:       "pd-extreme",
			iops:           proto.Int64(20000),
			wantIOPS:       proto.Int64(20000),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceDerived,
			wantAdjustable: true,
		},
		{
			name:           "pd-ssd performance is derived and not reported",
			diskType:       "pd-ssd",
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "pd-balanced performance is derived and not reported",
			diskType:       "pd-balanced",
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "Hyperdisk Balanced missing IOPS and throughput stays unknown, not zero",
			diskType:       "hyperdisk-balanced",
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
		{
			name:           "unrecognised disk type is unavailable",
			diskType:       "hyperdisk-future",
			iops:           proto.Int64(4000),
			throughput:     proto.Int64(200),
			wantIOPS:       proto.Int64(4000),
			wantThroughput: proto.Float64(200),
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := gcloudDiskResource(&computepb.Disk{
				SizeGb:                proto.Int64(200),
				ProvisionedIops:       tt.iops,
				ProvisionedThroughput: tt.throughput,
				Users:                 []string{testGcloudInstance},
			}, tt.diskType)

			config, ok := gcloudDiskConfigFromResource(resource)
			require.True(t, ok)

			assert.Equal(t, "GCP", config.Provider)
			assert.Equal(t, testGcloudProject, config.AccountID)
			assert.Equal(t, "us-central1", config.Region)
			assert.Equal(t, testGcloudZone, config.Zone)
			assert.Equal(t, testGcloudDiskURL, config.DiskID)
			assert.Equal(t, "data-disk-1", config.DiskName)
			assert.Equal(t, tt.diskType, config.DiskType)
			assert.Equal(t, proto.Float64(200), config.CapacityGiB)
			assert.Equal(t, tt.wantIOPS, config.ProvisionedIOPS)
			assert.Equal(t, tt.wantThroughput, config.ProvisionedThroughputMiBps)
			assert.Equal(t, tt.wantIOPSSource, config.IOPSSource)
			assert.Equal(t, tt.wantTputSource, config.ThroughputSource)
			assert.Equal(t, tt.wantAdjustable, config.HasAdjustablePerformance())
			assert.True(t, config.Attached)
			assert.Equal(t, []string{testGcloudInstance}, config.AttachedTo)
		})
	}
}

// Rows written before the collector added sizeGb/diskType, or read back without
// them, still carry the raw protobuf keys.
func TestGcloudDiskConfigFromResourceFallsBackToRawKeys(t *testing.T) {
	resource := gcloudDiskResource(&computepb.Disk{
		SizeGb:          proto.Int64(100),
		ProvisionedIops: proto.Int64(3000),
	}, "hyperdisk-extreme")
	delete(resource.Meta, "sizeGb")
	delete(resource.Meta, "diskType")

	config, ok := gcloudDiskConfigFromResource(resource)
	require.True(t, ok)

	assert.Equal(t, "hyperdisk-extreme", config.DiskType)
	assert.Equal(t, proto.Float64(100), config.CapacityGiB)
	assert.Equal(t, proto.Int64(3000), config.ProvisionedIOPS)
	assert.Equal(t, providers.DiskPerformanceExplicit, config.IOPSSource)
}

func TestGcloudDiskConfigFromResourceUnattachedAndMissingFields(t *testing.T) {
	resource := gcloudDiskResource(&computepb.Disk{}, "")
	resource.Id = "data-disk-1"
	delete(resource.Meta, "zone")

	config, ok := gcloudDiskConfigFromResource(resource)
	require.True(t, ok)

	assert.False(t, config.Attached)
	assert.Empty(t, config.AttachedTo)
	assert.Equal(t, "", config.AccountID)
	assert.Equal(t, "", config.Zone)
	assert.Equal(t, "", config.DiskType)
	assert.Nil(t, config.CapacityGiB)
	assert.Nil(t, config.ProvisionedIOPS)
	assert.Nil(t, config.ProvisionedThroughputMiBps)
	assert.Equal(t, providers.DiskPerformanceUnavailable, config.IOPSSource)
	assert.Equal(t, providers.DiskPerformanceUnavailable, config.ThroughputSource)
	assert.False(t, config.HasAdjustablePerformance())
}

func TestGcloudDiskConfigFromResourceRejectsNonDisks(t *testing.T) {
	tests := []struct {
		name     string
		resource providers.Resource
	}{
		{"compute instance", providers.Resource{ServiceName: "Compute Engine", Meta: map[string]any{"name": "vm-1"}}},
		{"disk with no meta", providers.Resource{ServiceName: ServiceNameDisk}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := gcloudDiskConfigFromResource(tt.resource)
			assert.False(t, ok)
		})
	}
}

func TestGcloudProjectFromResourceID(t *testing.T) {
	assert.Equal(t, "my-project", gcloudProjectFromResourceID(testGcloudDiskURL))
	assert.Equal(t, "my-project", gcloudProjectFromResourceID("projects/my-project/zones/us-central1-a/disks/d"))
	assert.Equal(t, "", gcloudProjectFromResourceID("data-disk-1"))
	assert.Equal(t, "", gcloudProjectFromResourceID(""))
}
