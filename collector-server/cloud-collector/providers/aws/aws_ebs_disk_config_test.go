package aws

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ebsVolumeResource builds the resource the collector stores for a volume, with Meta
// produced the same way fetchVolumesAndSnapshots produces it.
func ebsVolumeResource(volume types.Volume) providers.Resource {
	return providers.Resource{
		Id:          aws.ToString(volume.VolumeId),
		Name:        aws.ToString(volume.VolumeId),
		Type:        getAwsServiceResourceType(ServiceNameEc2, "storage"),
		ServiceName: ServiceNameEc2,
		Region:      "us-east-1",
		Arn:         "arn:aws:ec2:us-east-1:123456789012:volume/" + aws.ToString(volume.VolumeId),
		Meta:        structToMap(volume),
	}
}

func TestEbsDiskConfigFromResource(t *testing.T) {
	attached := []types.VolumeAttachment{{InstanceId: aws.String("i-0abc"), State: types.VolumeAttachmentStateAttached}}

	tests := []struct {
		name           string
		volume         types.Volume
		wantIOPS       *int64
		wantThroughput *float64
		wantIOPSSource providers.DiskPerformanceSource
		wantTputSource providers.DiskPerformanceSource
		wantAdjustable bool
	}{
		{
			name:           "gp3 has explicit IOPS and throughput",
			volume:         types.Volume{VolumeType: types.VolumeTypeGp3, Iops: aws.Int32(10000), Throughput: aws.Int32(500)},
			wantIOPS:       aws.Int64(10000),
			wantThroughput: aws.Float64(500),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "io2 has explicit IOPS and derived throughput",
			volume:         types.Volume{VolumeType: types.VolumeTypeIo2, Iops: aws.Int32(20000)},
			wantIOPS:       aws.Int64(20000),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceDerived,
			wantAdjustable: true,
		},
		{
			name:           "io1 has explicit IOPS and derived throughput",
			volume:         types.Volume{VolumeType: types.VolumeTypeIo1, Iops: aws.Int32(4000)},
			wantIOPS:       aws.Int64(4000),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceDerived,
			wantAdjustable: true,
		},
		{
			name:           "gp2 keeps the reported baseline IOPS as derived",
			volume:         types.Volume{VolumeType: types.VolumeTypeGp2, Iops: aws.Int32(300)},
			wantIOPS:       aws.Int64(300),
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "st1 has no reported performance",
			volume:         types.Volume{VolumeType: types.VolumeTypeSt1},
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "sc1 has no reported performance",
			volume:         types.Volume{VolumeType: types.VolumeTypeSc1},
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "gp3 missing IOPS and throughput stays unknown, not zero",
			volume:         types.Volume{VolumeType: types.VolumeTypeGp3},
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
		{
			name:           "unrecognised volume type is unavailable",
			volume:         types.Volume{VolumeType: types.VolumeType("gp9"), Iops: aws.Int32(5000), Throughput: aws.Int32(250)},
			wantIOPS:       aws.Int64(5000),
			wantThroughput: aws.Float64(250),
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
		{
			name:           "missing volume type is unavailable",
			volume:         types.Volume{},
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.volume.VolumeId = aws.String("vol-0123")
			tt.volume.Size = aws.Int32(500)
			tt.volume.AvailabilityZone = aws.String("us-east-1a")
			tt.volume.State = types.VolumeStateInUse
			tt.volume.Attachments = attached

			config, ok := ebsDiskConfigFromResource(ebsVolumeResource(tt.volume))
			require.True(t, ok)

			assert.Equal(t, "AWS", config.Provider)
			assert.Equal(t, "123456789012", config.AccountID)
			assert.Equal(t, "us-east-1", config.Region)
			assert.Equal(t, "us-east-1a", config.Zone)
			assert.Equal(t, "vol-0123", config.DiskID)
			assert.Equal(t, "vol-0123", config.DiskName)
			assert.Equal(t, string(tt.volume.VolumeType), config.DiskType)
			assert.Equal(t, aws.Float64(500), config.CapacityGiB)
			assert.Equal(t, tt.wantIOPS, config.ProvisionedIOPS)
			assert.Equal(t, tt.wantThroughput, config.ProvisionedThroughputMiBps)
			assert.Equal(t, tt.wantIOPSSource, config.IOPSSource)
			assert.Equal(t, tt.wantTputSource, config.ThroughputSource)
			assert.Equal(t, tt.wantAdjustable, config.HasAdjustablePerformance())
			assert.True(t, config.Attached)
			assert.Equal(t, []string{"i-0abc"}, config.AttachedTo)
		})
	}
}

func TestEbsDiskConfigFromResourceUnattachedAndMissingFields(t *testing.T) {
	resource := ebsVolumeResource(types.Volume{
		VolumeId:   aws.String("vol-0999"),
		VolumeType: types.VolumeTypeGp3,
		State:      types.VolumeStateAvailable,
	})
	resource.Arn = ""

	config, ok := ebsDiskConfigFromResource(resource)
	require.True(t, ok)

	assert.False(t, config.Attached)
	assert.Empty(t, config.AttachedTo)
	assert.Nil(t, config.CapacityGiB)
	assert.Nil(t, config.ProvisionedIOPS)
	assert.Nil(t, config.ProvisionedThroughputMiBps)
	assert.Equal(t, "", config.AccountID)
	assert.Equal(t, "", config.Zone)
}

func TestEbsDiskConfigFromResourceInUseWithoutAttachmentList(t *testing.T) {
	config, ok := ebsDiskConfigFromResource(ebsVolumeResource(types.Volume{
		VolumeId:   aws.String("vol-0777"),
		VolumeType: types.VolumeTypeGp3,
		State:      types.VolumeStateInUse,
	}))
	require.True(t, ok)
	assert.True(t, config.Attached)
	assert.Empty(t, config.AttachedTo)
}

func TestEbsDiskConfigFromResourceRejectsNonVolumes(t *testing.T) {
	tests := []struct {
		name     string
		resource providers.Resource
	}{
		{"compute instance", providers.Resource{Type: "compute-instance", Meta: map[string]any{"InstanceId": "i-0abc"}}},
		{"snapshot", providers.Resource{Type: "snapshot", Meta: map[string]any{"SnapshotId": "snap-0abc"}}},
		{"volume with no meta", providers.Resource{Type: "storage"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := ebsDiskConfigFromResource(tt.resource)
			assert.False(t, ok)
		})
	}
}

func TestAccountIDFromArn(t *testing.T) {
	assert.Equal(t, "123456789012", accountIDFromArn("arn:aws:ec2:us-east-1:123456789012:volume/vol-0123"))
	assert.Equal(t, "", accountIDFromArn("arn:aws:s3:::my-bucket"))
	assert.Equal(t, "", accountIDFromArn("vol-0123"))
	assert.Equal(t, "", accountIDFromArn(""))
}
