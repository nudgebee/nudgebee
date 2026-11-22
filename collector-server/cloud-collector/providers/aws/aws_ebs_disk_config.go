package aws

import (
	"strings"

	"nudgebee/collector/cloud/providers"
	"nudgebee/collector/cloud/providers/constants"
)

// ebsVolumeTypePerformance lists the EBS volume types we recognise and which of
// their performance settings can be changed without changing size or type.
// gp3 takes IOPS and throughput independently; io1/io2 take IOPS only, their
// throughput following from it. gp2 performance scales with size, and st1, sc1 and
// standard (magnetic) have no configurable performance at all.
var ebsVolumeTypePerformance = map[string]providers.DiskTypePerformance{
	"gp3":      {IOPSAdjustable: true, ThroughputAdjustable: true},
	"io1":      {IOPSAdjustable: true},
	"io2":      {IOPSAdjustable: true},
	"gp2":      {},
	"st1":      {},
	"sc1":      {},
	"standard": {},
}

// ebsDiskConfigFromResource builds the provider-neutral disk configuration of an EBS
// volume from the DescribeVolumes entry the collector stored in Meta. It returns
// false for anything that is not a stored volume. EBS already reports size in GiB
// and throughput in MiB/s, so no unit conversion is needed.
func ebsDiskConfigFromResource(resource providers.Resource) (providers.DiskConfig, bool) {
	if resource.Type != "storage" || len(resource.Meta) == 0 {
		return providers.DiskConfig{}, false
	}

	volumeType := providers.MetaString(resource, "Meta.VolumeType")
	performance, known := ebsVolumeTypePerformance[strings.ToLower(volumeType)]

	config := providers.DiskConfig{
		Provider:                   constants.ProviderAWS,
		AccountID:                  accountIDFromArn(resource.Arn),
		Region:                     resource.Region,
		Zone:                       providers.MetaString(resource, "Meta.AvailabilityZone"),
		DiskID:                     resource.Id,
		DiskName:                   resource.Name,
		DiskType:                   volumeType,
		CapacityGiB:                providers.PositiveMetaFloat(resource, "Meta.Size"),
		ProvisionedThroughputMiBps: providers.PositiveMetaFloat(resource, "Meta.Throughput"),
		CreatedAt:                  resource.CreatedAt,
	}
	if iops := providers.PositiveMetaFloat(resource, "Meta.Iops"); iops != nil {
		value := int64(*iops)
		config.ProvisionedIOPS = &value
	}
	config.IOPSSource = providers.DiskPerformanceSourceFor(known, performance.IOPSAdjustable, config.ProvisionedIOPS != nil)
	config.ThroughputSource = providers.DiskPerformanceSourceFor(known, performance.ThroughputAdjustable, config.ProvisionedThroughputMiBps != nil)

	if attachments, ok := resource.Meta["Attachments"].([]any); ok {
		for _, attachment := range attachments {
			if fields, ok := attachment.(map[string]any); ok {
				if instanceID, ok := fields["InstanceId"].(string); ok && instanceID != "" {
					config.AttachedTo = append(config.AttachedTo, instanceID)
				}
			}
		}
	}
	config.Attached = len(config.AttachedTo) > 0 || providers.MetaString(resource, "Meta.State") == "in-use"

	return config, true
}

// accountIDFromArn returns the account id of an ARN
// (arn:partition:service:region:account-id:resource), or "" if it has none.
func accountIDFromArn(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return ""
	}
	return parts[4]
}
