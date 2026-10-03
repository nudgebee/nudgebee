package opencostengine

import "strings"

// Storage list prices in $/GB/month, keyed by canonical provider and disk
// type. Same rates and resolution ladder as the PVC savings producers
// (api-server/services/scan_orchestrator/storage_pricing.go and the two
// python copies) — keep all copies in sync when a rate changes, so
// displayed storage cost and recommendation savings can never disagree on
// the same volume.
//
// fallbackStorageRatePerGBMonth is the last-resort rate when neither the
// storage class nor the provider can be resolved (on-prem, deleted class).
const fallbackStorageRatePerGBMonth = 0.10

// hoursPerMonth converts $/GB/month to the $/GB/hour figure OpenCost's
// models.PV.Cost expects (0.04/730 reproduces the old flat default).
const hoursPerMonth = 730.0

var storageRatesPerGBMonth = map[string]map[string]float64{
	"gcp": {
		"pd-standard": 0.04,
		"pd-balanced": 0.10,
		"pd-ssd":      0.17,
		"pd-extreme":  0.125,
		// Capacity only. Unlike pd-*, hyperdisk also bills provisioned IOPS
		// and throughput above baseline, so deleting one saves more than this.
		"hyperdisk-balanced": 0.08,
	},
	"aws": {
		"gp2":      0.10,
		"gp3":      0.08,
		"io1":      0.125,
		"io2":      0.125,
		"st1":      0.045,
		"sc1":      0.015,
		"standard": 0.05,
	},
	// Azure rates are the per-GiB fallback used when the disk size is
	// unknown; sized disks in the tiered families resolve through
	// azureDiskTiers instead. premiumv2_lrs and ultrassd_lrs have no size
	// bands — Azure meters them per provisioned GiB, so these are their
	// real rates (Provisioned Capacity, $/GiB/hour x 730).
	"azure": {
		"standard_lrs":    0.04,
		"standardssd_lrs": 0.075,
		"standardssd_zrs": 0.1125,
		"premium_lrs":     0.15,
		"premium_zrs":     0.225,
		"premiumv2_lrs":   0.08,
		"ultrassd_lrs":    0.12,
	},
}

// providerDefaultDiskType is the managed-K8s default storage class's disk
// type: GKE standard-rwo → pd-balanced, EKS gp2, AKS managed-csi →
// StandardSSD_LRS.
var providerDefaultDiskType = map[string]string{
	"gcp":   "pd-balanced",
	"aws":   "gp2",
	"azure": "standardssd_lrs",
}

// azureDiskTier is one Azure managed-disk provisioned size band. Azure
// bills a fixed monthly price per band and rounds every disk up into the
// next one, so a 100 GiB Premium disk is billed as P10 (128 GiB) — the
// reason a flat $/GB rate understates Azure volume cost.
type azureDiskTier struct {
	SizeGiB    float64
	MonthlyUSD float64
	Name       string
}

// azureDiskTiers holds the tiered Azure SKU families, eastus list prices
// from the retail prices API (the whole-disk "<tier> <redundancy> Disk"
// meter, unit 1/Month — not the much cheaper "Disk Mount" shared-disk
// meter). Standard HDD is LRS-only. Premium SSD v2 and Ultra are absent on
// purpose: Azure bills those per provisioned GiB with no size bands.
var azureDiskTiers = map[string][]azureDiskTier{
	"standard_lrs": {
		{32, 1.536, "S4"},
		{64, 3.008, "S6"},
		{128, 5.888, "S10"},
		{256, 11.328, "S15"},
		{512, 21.76, "S20"},
		{1024, 40.96, "S30"},
		{2048, 77.824, "S40"},
		{4096, 143.36, "S50"},
		{8192, 262.14, "S60"},
		{16384, 491.52, "S70"},
		{32767, 953.55, "S80"},
	},
	"standardssd_lrs": {
		{4, 0.3, "E1"},
		{8, 0.6, "E2"},
		{16, 1.2, "E3"},
		{32, 2.4, "E4"},
		{64, 4.8, "E6"},
		{128, 9.6, "E10"},
		{256, 19.2, "E15"},
		{512, 38.4, "E20"},
		{1024, 76.8, "E30"},
		{2048, 153.6, "E40"},
		{4096, 307.2, "E50"},
		{8192, 614.4, "E60"},
		{16384, 1228.8, "E70"},
		{32767, 2457.6, "E80"},
	},
	"standardssd_zrs": {
		{4, 0.45, "E1"},
		{8, 0.9, "E2"},
		{16, 1.8, "E3"},
		{32, 3.6, "E4"},
		{64, 7.2, "E6"},
		{128, 14.4, "E10"},
		{256, 28.8, "E15"},
		{512, 57.6, "E20"},
		{1024, 115.2, "E30"},
		{2048, 230.4, "E40"},
		{4096, 460.8, "E50"},
		{8192, 921.6, "E60"},
		{16384, 1843.2, "E70"},
		{32767, 3686.4, "E80"},
	},
	"premium_lrs": {
		{4, 0.6, "P1"},
		{8, 1.2, "P2"},
		{16, 2.4, "P3"},
		{32, 5.2795, "P4"},
		{64, 10.207, "P6"},
		{128, 19.71, "P10"},
		{256, 38.012142, "P15"},
		{512, 73.22, "P20"},
		{1024, 135.17, "P30"},
		{2048, 259.0457, "P40"},
		{4096, 495.5657, "P50"},
		{8192, 946.08, "P60"},
		{16384, 1802.06, "P70"},
		{32767, 3604.11, "P80"},
	},
	"premium_zrs": {
		{4, 0.9, "P1"},
		{8, 1.8, "P2"},
		{16, 3.6, "P3"},
		{32, 7.919, "P4"},
		{64, 15.31, "P6"},
		{128, 29.565, "P10"},
		{256, 57.018, "P15"},
		{512, 109.81, "P20"},
		{1024, 202.73, "P30"},
		{2048, 388.57, "P40"},
		{4096, 743.35, "P50"},
		{8192, 1419.12, "P60"},
		{16384, 2703.09, "P70"},
		{32767, 5406.16, "P80"},
	},
}

// azureTierMonthlyCost returns the monthly price Azure charges for a disk of
// sizeGB in the given SKU family, and whether that family is tiered at all.
func azureTierMonthlyCost(diskType string, sizeGB float64) (azureDiskTier, bool) {
	tiers, ok := azureDiskTiers[diskType]
	if !ok || sizeGB <= 0 {
		return azureDiskTier{}, false
	}
	for _, t := range tiers {
		if sizeGB <= t.SizeGiB {
			return t, true
		}
	}
	// Larger than Azure's biggest disk; bill it as the top band.
	return tiers[len(tiers)-1], true
}

// wellKnownClassDiskType resolves the default class names each managed
// provider ships when StorageClass.parameters carry no disk type. Keyed by
// provider so an on-prem class that happens to be named "standard" is not
// priced as a GCP disk.
var wellKnownClassDiskType = map[string]map[string]string{
	"gcp": {
		"standard":     "pd-standard",
		"standard-rwo": "pd-balanced",
		"premium-rwo":  "pd-ssd",
	},
	"aws": {
		"gp2": "gp2",
		"gp3": "gp3",
	},
	"azure": {
		"default":         "standardssd_lrs",
		"managed":         "standard_lrs",
		"managed-csi":     "standardssd_lrs",
		"managed-premium": "premium_lrs",
	},
}

// resolveStorageRatePerGBMonth resolves a PV's monthly $/GB rate from its
// StorageClass parameters (type / skuName — OpenCost's cost model passes
// them into GetPVKey), then the well-known class name, then the provider's
// default disk type, then the flat fallback. provider is the cluster's
// canonical cloud ("aws"/"gcp"/"azure") from DownloadPricingData.
func resolveStorageRatePerGBMonth(className string, parameters map[string]string, provider string, sizeGB float64) float64 {
	provider = strings.ToLower(provider)

	// azureRate rewrites the flat per-GB rate into the effective rate for
	// the band Azure actually bills, so OpenCost's rate x bytes arithmetic
	// yields the disk's real monthly cost. No-op unless the SKU is a tiered
	// Azure family and the PV's capacity is known.
	azureRate := func(diskType string, rate float64) float64 {
		if provider != "azure" {
			return rate
		}
		if tier, ok := azureTierMonthlyCost(diskType, sizeGB); ok {
			return tier.MonthlyUSD / sizeGB
		}
		return rate
	}

	if provider != "" && parameters != nil {
		diskType := parameters["type"]
		if diskType == "" {
			diskType = parameters["skuName"]
		}
		diskType = strings.ToLower(strings.TrimSpace(diskType))
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return azureRate(diskType, rate)
		}
	}
	if provider != "" && className != "" {
		if diskType, ok := wellKnownClassDiskType[provider][className]; ok {
			if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
				return azureRate(diskType, rate)
			}
		}
	}
	if diskType, ok := providerDefaultDiskType[provider]; ok {
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return azureRate(diskType, rate)
		}
	}
	return fallbackStorageRatePerGBMonth
}
