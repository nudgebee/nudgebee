package scan_orchestrator

import "strings"

// Storage list prices in $/GB/month, keyed by canonical provider and disk
// type. Same rates the cloud-collector's disk rules use
// (providers/gcloud/gcloud_disk.go, providers/azure/azure_disk.go). The
// python producers (k8s-collector storage_pricing.py, ml-k8s-server
// storage_pricing.py) carry the same tables and resolution ladder — keep
// all copies in sync when a rate changes.
//
// fallbackStorageRatePerGBMonth is the last-resort rate when neither the
// storage class nor the provider can be resolved (on-prem, deleted class).
const fallbackStorageRatePerGBMonth = 0.10

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

// azureDiskTier is one Azure managed-disk provisioned size band. Azure
// bills a fixed monthly price per band and rounds every disk up into the
// next one, so a 100 GiB Premium disk is billed as P10 (128 GiB) — the
// reason a flat $/GB rate understates Azure volume savings.
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

// providerDefaultDiskType is the managed-K8s default storage class's disk
// type: GKE standard-rwo → pd-balanced, EKS gp2, AKS managed-csi →
// StandardSSD_LRS.
var providerDefaultDiskType = map[string]string{
	"gcp":   "pd-balanced",
	"aws":   "gp2",
	"azure": "standardssd_lrs",
}

// wellKnownClassDiskType resolves the default class names each managed
// provider ships when StorageClass.parameters are unavailable. Keyed by
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

// storagePricing is embedded into the recommendation JSONB (key "pricing")
// so fallback-priced rows are distinguishable from resolved ones.
type storagePricing struct {
	PricePerGB float64 `json:"price_per_gb"`
	DiskType   string  `json:"disk_type,omitempty"`
	Provider   string  `json:"provider,omitempty"`
	Source     string  `json:"source"` // parameters | class_name | provider_default | fallback
	// Tier and TierMonthlyUSD are set only for tiered Azure disks, so a
	// row's savings can be reconciled against the Azure bill line.
	Tier           string  `json:"tier,omitempty"`
	TierMonthlyUSD float64 `json:"tier_monthly_usd,omitempty"`
}

// applyAzureTierPricing rewrites the flat per-GB rate into the effective
// rate for the band Azure actually bills (tier price / requested size), so
// every caller's existing `rate x size` arithmetic yields the real monthly
// cost. No-op for non-Azure disks, untiered SKUs, or unknown size.
func applyAzureTierPricing(p storagePricing, sizeGB float64) storagePricing {
	if p.Provider != "azure" {
		return p
	}
	tier, ok := azureTierMonthlyCost(p.DiskType, sizeGB)
	if !ok {
		return p
	}
	p.PricePerGB = tier.MonthlyUSD / sizeGB
	p.Tier = tier.Name
	p.TierMonthlyUSD = tier.MonthlyUSD
	return p
}

// resolveStoragePricing resolves a PV's monthly $/GB rate. Ladder:
// StorageClass parameters (type / skuName) → well-known class name →
// provider default (provider from provisioner / CSI driver / provisioned-by
// annotation, with accountProvider — canonical "aws"/"gcp"/"azure" from
// agent telemetry — as the backstop) → flat fallback. Deterministic on its
// inputs; the python producers implement the identical ladder.
func resolveStoragePricing(pv map[string]any, storageClasses map[string]map[string]any, accountProvider string, sizeGB float64) storagePricing {
	spec := getMapField(pv, "spec")
	className := getStringField(spec, "storage_class_name", "storageClassName")

	var sc map[string]any
	if className != "" {
		sc = storageClasses[className]
	}

	provider := providerFromProvisioner(getStringField(sc, "provisioner"))
	if provider == "" {
		provider = providerFromProvisioner(getStringField(getMapField(spec, "csi"), "driver"))
	}
	if provider == "" {
		annotations := getMapField(getMapField(pv, "metadata"), "annotations")
		provider = providerFromProvisioner(getStringField(annotations, "pv.kubernetes.io/provisioned-by"))
	}
	if provider == "" {
		provider = strings.ToLower(accountProvider)
	}

	if params := getMapField(sc, "parameters"); params != nil && provider != "" {
		diskType := normalizeDiskType(getStringField(params, "type", "skuName", "sku_name"))
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return applyAzureTierPricing(storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "parameters"}, sizeGB)
		}
	}
	if provider != "" && className != "" {
		if diskType, ok := wellKnownClassDiskType[provider][className]; ok {
			if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
				return applyAzureTierPricing(storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "class_name"}, sizeGB)
			}
		}
	}
	if diskType, ok := providerDefaultDiskType[provider]; ok {
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return applyAzureTierPricing(storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "provider_default"}, sizeGB)
		}
	}
	return storagePricing{PricePerGB: fallbackStorageRatePerGBMonth, Source: "fallback"}
}

func providerFromProvisioner(s string) string {
	s = strings.ToLower(s)
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "gke.io"), strings.Contains(s, "gce-pd"):
		return "gcp"
	case strings.Contains(s, "ebs.csi.aws.com"), strings.Contains(s, "aws-ebs"):
		return "aws"
	case strings.Contains(s, "azure"):
		return "azure"
	}
	return ""
}

// normalizeDiskType lowercases so Azure skuName values (Premium_LRS,
// StandardSSD_LRS) match the rate-table keys; GCP/AWS types are already
// lowercase.
func normalizeDiskType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// getStringField returns the first non-empty string value at the given
// key aliases (snake_case / camelCase agents both occur).
func getStringField(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
