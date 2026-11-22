package azure

import (
	"strings"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
)

// azureDiskSKUPerformance lists the managed disk SKUs we recognise, keyed by
// lower-cased sku.name, and which of their performance settings can be changed
// without changing size or SKU. Only Premium SSD v2 and Ultra Disk take IOPS and
// throughput independently; every other SKU gets both from its size tier.
var azureDiskSKUPerformance = map[string]providers.DiskTypePerformance{
	"premiumv2_lrs":   {IOPSAdjustable: true, ThroughputAdjustable: true},
	"ultrassd_lrs":    {IOPSAdjustable: true, ThroughputAdjustable: true},
	"premium_lrs":     {},
	"premium_zrs":     {},
	"standardssd_lrs": {},
	"standardssd_zrs": {},
	"standard_lrs":    {},
}

// azureDiskConfigFromResource builds the provider-neutral disk configuration of a
// managed disk from the armcompute.Disk the collector stored in Meta. It returns
// false for anything that is not a stored managed disk.
//
// Azure reports diskSizeGB in GiB despite the name. diskMBpsReadWrite is decimal
// megabytes per second — the armcompute model documents it as "MB here uses the
// ISO notation, of powers of 10" — so it is converted to MiB/s.
func azureDiskConfigFromResource(resource providers.Resource) (providers.DiskConfig, bool) {
	if !strings.EqualFold(resource.Type, "Microsoft.Compute/disks") || len(resource.Meta) == 0 {
		return providers.DiskConfig{}, false
	}

	sku := providers.MetaString(resource, "Meta.sku.name")
	performance, known := azureDiskSKUPerformance[strings.ToLower(sku)]

	config := providers.DiskConfig{
		Provider:    constants.ProviderAzure,
		AccountID:   extractSubscriptionIdFromResourceId(resource.Id),
		Region:      resource.Region,
		DiskID:      resource.Id,
		DiskName:    resource.Name,
		DiskType:    sku,
		CapacityGiB: providers.PositiveMetaFloat(resource, "Meta.properties.diskSizeGB"),
		CreatedAt:   resource.CreatedAt,
	}
	if zones := providers.MetaStrings(resource, "Meta.zones"); len(zones) > 0 {
		config.Zone = zones[0]
	}
	if iops := providers.PositiveMetaFloat(resource, "Meta.properties.diskIOPSReadWrite"); iops != nil {
		value := int64(*iops)
		config.ProvisionedIOPS = &value
	}
	if mbps := providers.PositiveMetaFloat(resource, "Meta.properties.diskMBpsReadWrite"); mbps != nil {
		value := providers.MBpsToMiBps(*mbps)
		config.ProvisionedThroughputMiBps = &value
	}
	config.IOPSSource = providers.DiskPerformanceSourceFor(known, performance.IOPSAdjustable, config.ProvisionedIOPS != nil)
	config.ThroughputSource = providers.DiskPerformanceSourceFor(known, performance.ThroughputAdjustable, config.ProvisionedThroughputMiBps != nil)

	if managedBy := providers.MetaString(resource, "Meta.managedBy"); managedBy != "" {
		config.AttachedTo = append(config.AttachedTo, managedBy)
	}
	config.AttachedTo = append(config.AttachedTo, providers.MetaStrings(resource, "Meta.managedByExtended")...)
	config.Attached = len(config.AttachedTo) > 0 || providers.MetaString(resource, "Meta.properties.diskState") == "Attached"

	return config, true
}
