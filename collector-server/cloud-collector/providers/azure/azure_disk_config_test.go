package azure

import (
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAzureDiskID = "/subscriptions/11111111-2222-3333-4444-555555555555/resourceGroups/rg-data/providers/Microsoft.Compute/disks/data-disk-1"
	testAzureVMID   = "/subscriptions/11111111-2222-3333-4444-555555555555/resourceGroups/rg-data/providers/Microsoft.Compute/virtualMachines/vm-1"
)

// azureDiskResource builds the resource the collector stores for a managed disk, with
// Meta produced the same way diskService.GetResources produces it.
func azureDiskResource(disk armcompute.Disk) providers.Resource {
	disk.ID = to.Ptr(testAzureDiskID)
	disk.Name = to.Ptr("data-disk-1")
	disk.Type = to.Ptr("Microsoft.Compute/disks")
	disk.Location = to.Ptr("eastus")
	return providers.Resource{
		Id:          *disk.ID,
		Name:        *disk.Name,
		Type:        *disk.Type,
		ServiceName: "Microsoft.Compute/disks",
		Region:      "eastus",
		Arn:         *disk.ID,
		Meta:        structToMap(disk),
	}
}

func azureDiskSKU(name string) *armcompute.DiskSKU {
	return &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypes(name))}
}

func TestAzureDiskConfigFromResource(t *testing.T) {
	tests := []struct {
		name           string
		sku            string
		iops           *int64
		mbps           *int64
		wantIOPS       *int64
		wantThroughput *float64
		wantIOPSSource providers.DiskPerformanceSource
		wantTputSource providers.DiskPerformanceSource
		wantAdjustable bool
	}{
		{
			name:           "Premium SSD v2 has explicit IOPS and throughput",
			sku:            "PremiumV2_LRS",
			iops:           to.Ptr(int64(8000)),
			mbps:           to.Ptr(int64(300)),
			wantIOPS:       to.Ptr(int64(8000)),
			wantThroughput: to.Ptr(286.102294921875),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "Ultra Disk has explicit IOPS and throughput",
			sku:            "UltraSSD_LRS",
			iops:           to.Ptr(int64(50000)),
			mbps:           to.Ptr(int64(1000)),
			wantIOPS:       to.Ptr(int64(50000)),
			wantThroughput: to.Ptr(953.67431640625),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "Premium SSD keeps its tier performance as derived",
			sku:            "Premium_LRS",
			iops:           to.Ptr(int64(1100)),
			mbps:           to.Ptr(int64(125)),
			wantIOPS:       to.Ptr(int64(1100)),
			wantThroughput: to.Ptr(119.20928955078125),
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "Standard SSD with nothing reported is derived and unknown",
			sku:            "StandardSSD_LRS",
			wantIOPSSource: providers.DiskPerformanceDerived,
			wantTputSource: providers.DiskPerformanceDerived,
		},
		{
			name:           "SKU match ignores case",
			sku:            "premiumv2_lrs",
			iops:           to.Ptr(int64(3000)),
			mbps:           to.Ptr(int64(125)),
			wantIOPS:       to.Ptr(int64(3000)),
			wantThroughput: to.Ptr(119.20928955078125),
			wantIOPSSource: providers.DiskPerformanceExplicit,
			wantTputSource: providers.DiskPerformanceExplicit,
			wantAdjustable: true,
		},
		{
			name:           "Premium SSD v2 missing IOPS and throughput stays unknown, not zero",
			sku:            "PremiumV2_LRS",
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
		{
			name:           "Premium SSD v2 reporting zero is treated as not set",
			sku:            "PremiumV2_LRS",
			iops:           to.Ptr(int64(0)),
			mbps:           to.Ptr(int64(0)),
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
		{
			name:           "unrecognised SKU is unavailable",
			sku:            "FutureSSD_LRS",
			iops:           to.Ptr(int64(5000)),
			mbps:           to.Ptr(int64(200)),
			wantIOPS:       to.Ptr(int64(5000)),
			wantThroughput: to.Ptr(190.73486328125),
			wantIOPSSource: providers.DiskPerformanceUnavailable,
			wantTputSource: providers.DiskPerformanceUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := azureDiskResource(armcompute.Disk{
				SKU:       azureDiskSKU(tt.sku),
				ManagedBy: to.Ptr(testAzureVMID),
				Zones:     []*string{to.Ptr("2")},
				Properties: &armcompute.DiskProperties{
					DiskSizeGB:        to.Ptr(int32(512)),
					DiskIOPSReadWrite: tt.iops,
					DiskMBpsReadWrite: tt.mbps,
					DiskState:         to.Ptr(armcompute.DiskStateAttached),
				},
			})

			config, ok := azureDiskConfigFromResource(resource)
			require.True(t, ok)

			assert.Equal(t, "Azure", config.Provider)
			assert.Equal(t, "11111111-2222-3333-4444-555555555555", config.AccountID)
			assert.Equal(t, "eastus", config.Region)
			assert.Equal(t, "2", config.Zone)
			assert.Equal(t, testAzureDiskID, config.DiskID)
			assert.Equal(t, "data-disk-1", config.DiskName)
			assert.Equal(t, tt.sku, config.DiskType)
			assert.Equal(t, to.Ptr(512.0), config.CapacityGiB)
			assert.Equal(t, tt.wantIOPS, config.ProvisionedIOPS)
			if tt.wantThroughput == nil {
				assert.Nil(t, config.ProvisionedThroughputMiBps)
			} else {
				require.NotNil(t, config.ProvisionedThroughputMiBps)
				assert.InDelta(t, *tt.wantThroughput, *config.ProvisionedThroughputMiBps, 1e-9)
			}
			assert.Equal(t, tt.wantIOPSSource, config.IOPSSource)
			assert.Equal(t, tt.wantTputSource, config.ThroughputSource)
			assert.Equal(t, tt.wantAdjustable, config.HasAdjustablePerformance())
			assert.True(t, config.Attached)
			assert.Equal(t, []string{testAzureVMID}, config.AttachedTo)
		})
	}
}

func TestAzureDiskConfigFromResourceUnattachedAndMissingFields(t *testing.T) {
	resource := azureDiskResource(armcompute.Disk{
		Properties: &armcompute.DiskProperties{DiskState: to.Ptr(armcompute.DiskStateUnattached)},
	})

	config, ok := azureDiskConfigFromResource(resource)
	require.True(t, ok)

	assert.False(t, config.Attached)
	assert.Empty(t, config.AttachedTo)
	assert.Equal(t, "", config.DiskType)
	assert.Equal(t, "", config.Zone)
	assert.Nil(t, config.CapacityGiB)
	assert.Nil(t, config.ProvisionedIOPS)
	assert.Nil(t, config.ProvisionedThroughputMiBps)
	assert.Equal(t, providers.DiskPerformanceUnavailable, config.IOPSSource)
	assert.Equal(t, providers.DiskPerformanceUnavailable, config.ThroughputSource)
	assert.False(t, config.HasAdjustablePerformance())
}

func TestAzureDiskConfigFromResourceSharedDisk(t *testing.T) {
	secondVM := testAzureVMID + "-b"
	resource := azureDiskResource(armcompute.Disk{
		SKU:               azureDiskSKU("PremiumV2_LRS"),
		ManagedByExtended: []*string{to.Ptr(testAzureVMID), to.Ptr(secondVM)},
		Properties:        &armcompute.DiskProperties{DiskState: to.Ptr(armcompute.DiskStateAttached)},
	})

	config, ok := azureDiskConfigFromResource(resource)
	require.True(t, ok)
	assert.True(t, config.Attached)
	assert.Equal(t, []string{testAzureVMID, secondVM}, config.AttachedTo)
}

func TestAzureDiskConfigFromResourceRejectsNonDisks(t *testing.T) {
	tests := []struct {
		name     string
		resource providers.Resource
	}{
		{"virtual machine", providers.Resource{Type: "Microsoft.Compute/virtualMachines", Meta: map[string]any{"name": "vm-1"}}},
		{"disk with no meta", providers.Resource{Type: "Microsoft.Compute/disks"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := azureDiskConfigFromResource(tt.resource)
			assert.False(t, ok)
		})
	}
}
