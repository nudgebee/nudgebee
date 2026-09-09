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
	"azure": {
		"standard_lrs":    0.04,
		"standardssd_lrs": 0.075,
		"premium_lrs":     0.12,
		"premiumv2_lrs":   0.12,
		"ultrassd_lrs":    0.15,
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
func resolveStorageRatePerGBMonth(className string, parameters map[string]string, provider string) float64 {
	provider = strings.ToLower(provider)

	if provider != "" && parameters != nil {
		diskType := parameters["type"]
		if diskType == "" {
			diskType = parameters["skuName"]
		}
		diskType = strings.ToLower(strings.TrimSpace(diskType))
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return rate
		}
	}
	if provider != "" && className != "" {
		if diskType, ok := wellKnownClassDiskType[provider][className]; ok {
			if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
				return rate
			}
		}
	}
	if diskType, ok := providerDefaultDiskType[provider]; ok {
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return rate
		}
	}
	return fallbackStorageRatePerGBMonth
}
