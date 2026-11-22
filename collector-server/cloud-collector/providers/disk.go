package providers

import "time"

// DiskPerformanceSource says how a disk's IOPS or throughput setting is determined.
type DiskPerformanceSource string

const (
	// DiskPerformanceExplicit means the setting can be changed independently of the
	// disk's size and type, and the cloud reported its current value.
	DiskPerformanceExplicit DiskPerformanceSource = "explicit"
	// DiskPerformanceDerived means the setting follows from the disk's type, size or
	// tier and cannot be changed on its own. The value is filled only when the cloud
	// reports it; it is never computed here.
	DiskPerformanceDerived DiskPerformanceSource = "derived"
	// DiskPerformanceUnavailable means the setting could not be determined: the disk
	// type is not recognised, or an adjustable setting was not reported.
	DiskPerformanceUnavailable DiskPerformanceSource = "unavailable"
)

// DiskTypePerformance says which performance settings a disk type lets you set
// independently of its size.
type DiskTypePerformance struct {
	IOPSAdjustable       bool
	ThroughputAdjustable bool
}

// DiskPerformanceSourceFor classifies one performance setting of a disk. known is
// whether the disk type is recognised, adjustable whether that type lets the setting
// be changed on its own, and reported whether the cloud returned a value for it.
func DiskPerformanceSourceFor(known, adjustable, reported bool) DiskPerformanceSource {
	switch {
	case !known:
		return DiskPerformanceUnavailable
	case !adjustable:
		return DiskPerformanceDerived
	case reported:
		return DiskPerformanceExplicit
	default:
		return DiskPerformanceUnavailable
	}
}

// DiskConfig is the provisioned configuration of one cloud disk — an AWS EBS volume,
// an Azure managed disk or a GCP persistent disk — in provider-neutral units.
// Capacity, IOPS and throughput are pointers because a missing value is unknown,
// not zero.
type DiskConfig struct {
	Provider string `json:"provider"`
	// AccountID is the AWS account number, Azure subscription id or GCP project id.
	AccountID string `json:"account_id"`
	Region    string `json:"region"`
	Zone      string `json:"zone,omitempty"`
	// DiskID is the provider's canonical identity for the disk, the same value the
	// collector stores as Resource.Id.
	DiskID   string `json:"disk_id"`
	DiskName string `json:"disk_name"`
	// DiskType is the provider's own name, unchanged: gp3, PremiumV2_LRS, hyperdisk-balanced.
	DiskType                   string                `json:"disk_type"`
	CapacityGiB                *float64              `json:"capacity_gib,omitempty"`
	ProvisionedIOPS            *int64                `json:"provisioned_iops,omitempty"`
	ProvisionedThroughputMiBps *float64              `json:"provisioned_throughput_mibps,omitempty"`
	IOPSSource                 DiskPerformanceSource `json:"iops_source"`
	ThroughputSource           DiskPerformanceSource `json:"throughput_source"`
	Attached                   bool                  `json:"attached"`
	// AttachedTo holds the provider's identifiers of the instances using the disk.
	AttachedTo []string `json:"attached_to,omitempty"`
	// CreatedAt is when the disk was created in the cloud. Zero means unknown.
	CreatedAt time.Time `json:"created_at"`
}

// HasAdjustablePerformance reports whether the disk has an IOPS or throughput setting
// that can be lowered without changing its size or type.
func (c DiskConfig) HasAdjustablePerformance() bool {
	return c.IOPSSource == DiskPerformanceExplicit || c.ThroughputSource == DiskPerformanceExplicit
}

// DiskDataQuality describes how complete the measurements behind a
// DiskPerformanceAnalysis are.
type DiskDataQuality string

const (
	DiskDataQualityComplete DiskDataQuality = "complete"
	DiskDataQualityPartial  DiskDataQuality = "partial"
	DiskDataQualityNoData   DiskDataQuality = "no_data"
)

// DiskPerformanceAnalysis summarises a disk's observed performance over an
// observation window. The peaks are pointers because a disk with no measurements has
// an unknown peak, not a peak of zero.
type DiskPerformanceAnalysis struct {
	DiskID              string    `json:"disk_id"`
	PeakIOPS            *float64  `json:"peak_iops,omitempty"`
	PeakThroughputMiBps *float64  `json:"peak_throughput_mibps,omitempty"`
	ObservationStart    time.Time `json:"observation_start"`
	ObservationEnd      time.Time `json:"observation_end"`
	PeriodSeconds       int64     `json:"period_seconds"`
	// PeakResolutionSeconds is the length of time each measurement behind the peaks
	// covers: 60 where the cloud reports the busiest minute of each interval, the
	// whole interval where it reports only the interval's total or average. A peak
	// shorter than this is averaged down and can be missed.
	PeakResolutionSeconds int64 `json:"peak_resolution_seconds"`
	// CoveragePercent is the share of intervals in the window that had a measurement.
	CoveragePercent float64         `json:"coverage_percent"`
	DataQuality     DiskDataQuality `json:"data_quality"`
	// IOPSLimitExceeded and ThroughputLimitExceeded report whether the cloud itself
	// flagged the workload as driving more than the provisioned setting during the
	// window. Nil where the cloud does not report it. They are a supplementary
	// signal, not a measure of demand.
	IOPSLimitExceeded       *bool `json:"iops_limit_exceeded,omitempty"`
	ThroughputLimitExceeded *bool `json:"throughput_limit_exceeded,omitempty"`
}

const bytesPerMiB = 1024 * 1024

// MBpsToMiBps converts decimal megabytes per second (10^6 bytes) to MiB/s.
func MBpsToMiBps(mbps float64) float64 {
	return mbps * 1_000_000 / bytesPerMiB
}

// MiBpsToMBps converts MiB/s to decimal megabytes per second (10^6 bytes).
func MiBpsToMBps(mibps float64) float64 {
	return mibps * bytesPerMiB / 1_000_000
}

// PositiveMetaFloat reads a numeric value at a metadata path (see MetadataAccessor).
// It returns nil when the field is missing, not numeric, or not positive: capacity,
// IOPS and throughput are never legitimately zero, so zero means "not set".
func PositiveMetaFloat(resource Resource, path string) *float64 {
	value, err := NewMetadataAccessor().GetFloat(resource, path)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}

// MetaString reads a string value at a metadata path, returning "" when it is missing.
func MetaString(resource Resource, path string) string {
	value, err := NewMetadataAccessor().GetString(resource, path)
	if err != nil {
		return ""
	}
	return value
}

// MetaStrings reads a list of strings at a metadata path, skipping non-string and
// empty entries.
func MetaStrings(resource Resource, path string) []string {
	value, err := NewMetadataAccessor().Get(resource, path)
	if err != nil {
		return nil
	}
	var result []string
	switch items := value.(type) {
	case []string:
		for _, s := range items {
			if s != "" {
				result = append(result, s)
			}
		}
	case []any:
		for _, item := range items {
			if s, ok := item.(string); ok && s != "" {
				result = append(result, s)
			}
		}
	}
	return result
}
