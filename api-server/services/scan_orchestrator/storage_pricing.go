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
}

// resolveStoragePricing resolves a PV's monthly $/GB rate. Ladder:
// StorageClass parameters (type / skuName) → well-known class name →
// provider default (provider from provisioner / CSI driver / provisioned-by
// annotation, with accountProvider — canonical "aws"/"gcp"/"azure" from
// agent telemetry — as the backstop) → flat fallback. Deterministic on its
// inputs; the python producers implement the identical ladder.
func resolveStoragePricing(pv map[string]any, storageClasses map[string]map[string]any, accountProvider string) storagePricing {
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
			return storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "parameters"}
		}
	}
	if provider != "" && className != "" {
		if diskType, ok := wellKnownClassDiskType[provider][className]; ok {
			if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
				return storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "class_name"}
			}
		}
	}
	if diskType, ok := providerDefaultDiskType[provider]; ok {
		if rate, ok := storageRatesPerGBMonth[provider][diskType]; ok {
			return storagePricing{PricePerGB: rate, DiskType: diskType, Provider: provider, Source: "provider_default"}
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
