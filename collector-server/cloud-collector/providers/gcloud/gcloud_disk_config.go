package gcloud

import (
	"strings"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
)

// gcloudDiskTypePerformance lists the Compute Engine disk types we recognise and
// which of their performance settings can be changed without changing size or type.
// Hyperdisk Balanced (and its high-availability variant) takes IOPS and throughput
// independently; Hyperdisk Extreme and pd-extreme take IOPS only; Hyperdisk
// Throughput and Hyperdisk ML take throughput only. pd-standard, pd-balanced and
// pd-ssd get their performance from size and the attached VM, and Compute Engine
// reports no IOPS or throughput figure for them.
var gcloudDiskTypePerformance = map[string]providers.DiskTypePerformance{
	"hyperdisk-balanced":                   {IOPSAdjustable: true, ThroughputAdjustable: true},
	"hyperdisk-balanced-high-availability": {IOPSAdjustable: true, ThroughputAdjustable: true},
	"hyperdisk-extreme":                    {IOPSAdjustable: true},
	"hyperdisk-throughput":                 {ThroughputAdjustable: true},
	"hyperdisk-ml":                         {ThroughputAdjustable: true},
	"pd-extreme":                           {IOPSAdjustable: true},
	"pd-standard":                          {},
	"pd-balanced":                          {},
	"pd-ssd":                               {},
}

// gcloudDiskConfigFromResource builds the provider-neutral disk configuration of a
// Compute Engine disk from the computepb.Disk the collector stored in Meta. It
// returns false for anything that is not a stored disk.
//
// Meta is the protobuf struct passed through encoding/json, so its keys are
// snake_case (size_gb, provisioned_iops); the collector also adds sizeGb and
// diskType. Compute Engine sizes are GiB, and provisioned throughput is taken as
// MiB/s, the unit the Hyperdisk documentation uses.
func gcloudDiskConfigFromResource(resource providers.Resource) (providers.DiskConfig, bool) {
	if resource.ServiceName != ServiceNameDisk || len(resource.Meta) == 0 {
		return providers.DiskConfig{}, false
	}

	diskType := providers.MetaString(resource, "Meta.diskType")
	if diskType == "" {
		diskType = lastPathSegment(providers.MetaString(resource, "Meta.type"))
	}
	performance, known := gcloudDiskTypePerformance[strings.ToLower(diskType)]

	capacity := providers.PositiveMetaFloat(resource, "Meta.sizeGb")
	if capacity == nil {
		capacity = providers.PositiveMetaFloat(resource, "Meta.size_gb")
	}

	config := providers.DiskConfig{
		Provider:                   constants.ProviderGCP,
		AccountID:                  gcloudProjectFromResourceID(resource.Id),
		Region:                     resource.Region,
		Zone:                       lastPathSegment(providers.MetaString(resource, "Meta.zone")),
		DiskID:                     resource.Id,
		DiskName:                   resource.Name,
		DiskType:                   diskType,
		CapacityGiB:                capacity,
		ProvisionedThroughputMiBps: providers.PositiveMetaFloat(resource, "Meta.provisioned_throughput"),
		AttachedTo:                 providers.MetaStrings(resource, "Meta.users"),
		CreatedAt:                  resource.CreatedAt,
	}
	if iops := providers.PositiveMetaFloat(resource, "Meta.provisioned_iops"); iops != nil {
		value := int64(*iops)
		config.ProvisionedIOPS = &value
	}
	config.IOPSSource = providers.DiskPerformanceSourceFor(known, performance.IOPSAdjustable, config.ProvisionedIOPS != nil)
	config.ThroughputSource = providers.DiskPerformanceSourceFor(known, performance.ThroughputAdjustable, config.ProvisionedThroughputMiBps != nil)
	config.Attached = len(config.AttachedTo) > 0

	return config, true
}

// gcloudProjectFromResourceID returns the project id from a Compute Engine resource
// URL or path (…/projects/<project>/zones/…), or "" if it has none.
func gcloudProjectFromResourceID(resourceID string) string {
	_, rest, found := strings.Cut(resourceID, "projects/")
	if !found {
		return ""
	}
	project, _, _ := strings.Cut(rest, "/")
	return project
}
