package opencostengine

import (
	"strconv"
	"testing"

	"github.com/opencost/opencost/core/pkg/clustercache"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResolveStorageRatePerGBMonth(t *testing.T) {
	cases := []struct {
		name       string
		className  string
		parameters map[string]string
		provider   string
		sizeGB     float64
		want       float64
	}{
		{"gcp parameters type", "custom-ssd", map[string]string{"type": "pd-ssd"}, "gcp", 0, 0.17},
		{"azure skuName casing", "fast", map[string]string{"skuName": "Premium_LRS"}, "azure", 0, 0.15},
		{"gcp hyperdisk balanced", "hyperdisk-balanced-rwo", map[string]string{"type": "hyperdisk-balanced"}, "gcp", 0, 0.08},
		{"well-known gke standard", "standard", nil, "gcp", 0, 0.04},
		{"well-known aks managed-premium", "managed-premium", nil, "azure", 0, 0.15},
		{"unknown class provider default", "who-knows", nil, "aws", 0, 0.10},
		{"unknown parameters provider default", "x", map[string]string{"type": "exotic"}, "aws", 0, 0.10},
		{"no provider fallback", "standard", nil, "", 0, fallbackStorageRatePerGBMonth},
		{"unsupported provider fallback", "standard", nil, "civo", 0, fallbackStorageRatePerGBMonth},
		// Sized Azure disks price off the band Azure bills, so rate x size
		// is the disk's real monthly cost: 100 GiB Premium => P10 $19.71.
		{"azure sized premium rounds up to P10", "fast", map[string]string{"skuName": "Premium_LRS"}, "azure", 100, 19.71 / 100},
		{"azure sized standardssd", "managed-csi", nil, "azure", 100, 9.6 / 100},
		{"azure per-gib v2 stays flat", "x", map[string]string{"skuName": "PremiumV2_LRS"}, "azure", 100, 0.08},
		{"gcp sized is unaffected", "custom-ssd", map[string]string{"type": "pd-ssd"}, "gcp", 100, 0.17},
	}
	for _, tc := range cases {
		if got := resolveStorageRatePerGBMonth(tc.className, tc.parameters, tc.provider, tc.sizeGB); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStorageRateMapsConsistent(t *testing.T) {
	// Every disk type the resolution maps can emit must have a rate — a
	// drifted entry would otherwise price storage at the fallback (or, in
	// the python copies of this ladder, raise). Guards future rate edits.
	for provider, classes := range wellKnownClassDiskType {
		for className, diskType := range classes {
			if _, ok := storageRatesPerGBMonth[provider][diskType]; !ok {
				t.Errorf("wellKnownClassDiskType[%s][%s] = %s has no rate", provider, className, diskType)
			}
		}
	}
	for provider, diskType := range providerDefaultDiskType {
		if _, ok := storageRatesPerGBMonth[provider][diskType]; !ok {
			t.Errorf("providerDefaultDiskType[%s] = %s has no rate", provider, diskType)
		}
	}
}

func TestPVPricing_StorageClassAware(t *testing.T) {
	np := &NudgebeeProvider{CloudProvider: "gcp"}
	pv, err := np.PVPricing(&nudgebeePVKey{
		StorageClassName: "standard",
	})
	if err != nil {
		t.Fatal(err)
	}
	// pd-standard: $0.04/GB/month → $/GB/hour, same figure the old flat
	// default hardcoded for every PV.
	if pv.Cost != "0.00005479452" {
		t.Errorf("Cost = %q; want 0.00005479452", pv.Cost)
	}

	pv, err = np.PVPricing(&nudgebeePVKey{
		StorageClassName: "fast",
		Parameters:       map[string]string{"type": "pd-ssd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.FormatFloat(0.17/hoursPerMonth, 'f', 11, 64)
	if pv.Cost != want {
		t.Errorf("Cost = %q; want %q (pd-ssd)", pv.Cost, want)
	}
}

// Golden vectors for Azure tier pricing. The same cases are asserted in the
// other three producers (api-server, ml-k8s-server, k8s-collector); if a
// rate moves in one copy and not the others, these diverge.
func TestAzureTierMonthlyCost_Golden(t *testing.T) {
	cases := []struct {
		diskType string
		sizeGB   float64
		tier     string
		monthly  float64
	}{
		{"premium_lrs", 100, "P10", 19.71},
		{"premium_lrs", 128, "P10", 19.71},
		{"premium_lrs", 129, "P15", 38.012142},
		{"premium_lrs", 1024, "P30", 135.17},
		{"premium_zrs", 100, "P10", 29.565},
		{"standardssd_lrs", 100, "E10", 9.6},
		{"standardssd_zrs", 512, "E20", 57.6},
		{"standard_lrs", 10, "S4", 1.536},
		{"standard_lrs", 40000, "S80", 953.55}, // above Azure's largest disk
	}
	for _, c := range cases {
		got, ok := azureTierMonthlyCost(c.diskType, c.sizeGB)
		if !ok || got.Name != c.tier || got.MonthlyUSD != c.monthly {
			t.Errorf("azureTierMonthlyCost(%s, %v) = %+v %v, want %s $%v",
				c.diskType, c.sizeGB, got, ok, c.tier, c.monthly)
		}
	}

	// Per-GiB SKUs have no bands and must not be tier-priced.
	for _, dt := range []string{"premiumv2_lrs", "ultrassd_lrs"} {
		if _, ok := azureTierMonthlyCost(dt, 100); ok {
			t.Errorf("%s must not be tiered", dt)
		}
	}
	if _, ok := azureTierMonthlyCost("premium_lrs", 0); ok {
		t.Error("size 0 must not resolve a tier")
	}
}

// The tier rate is only correct because OpenCost multiplies it back by the
// same capacity GetPVKey read off the PV, so exercise that whole seam: a
// 100 GiB Premium disk is billed as P10, and rate x capacity x 730h must
// come back out as the P10 monthly price.
func TestPVPricingUsesBilledTierForAzureDisks(t *testing.T) {
	np := &NudgebeeProvider{CloudProvider: "azure"}
	pv := &clustercache.PersistentVolume{
		Spec: v1.PersistentVolumeSpec{
			StorageClassName: "managed-premium",
			Capacity:         v1.ResourceList{v1.ResourceStorage: resource.MustParse("100Gi")},
		},
	}
	key := np.GetPVKey(pv, map[string]string{"skuName": "Premium_LRS"}, "eastus")
	if got := key.(*nudgebeePVKey).CapacityGB; got != 100 {
		t.Fatalf("CapacityGB = %v, want 100", got)
	}

	got, err := np.PVPricing(key)
	if err != nil {
		t.Fatalf("PVPricing: %v", err)
	}
	costPerGiBHour, err := strconv.ParseFloat(got.Cost, 64)
	if err != nil {
		t.Fatalf("parse cost %q: %v", got.Cost, err)
	}
	monthly := costPerGiBHour * 100 * hoursPerMonth
	if diff := monthly - 19.71; diff > 1e-6 || diff < -1e-6 {
		t.Errorf("monthly cost = %v, want the P10 price 19.71", monthly)
	}
}

// A PV with no capacity must degrade to the flat rate rather than divide by
// zero.
func TestPVPricingWithoutCapacityFallsBackToFlatRate(t *testing.T) {
	np := &NudgebeeProvider{CloudProvider: "azure"}
	pv := &clustercache.PersistentVolume{
		Spec: v1.PersistentVolumeSpec{StorageClassName: "managed-premium"},
	}
	key := np.GetPVKey(pv, map[string]string{"skuName": "Premium_LRS"}, "eastus")
	got, err := np.PVPricing(key)
	if err != nil {
		t.Fatalf("PVPricing: %v", err)
	}
	costPerGiBHour, err := strconv.ParseFloat(got.Cost, 64)
	if err != nil {
		t.Fatalf("parse cost %q: %v", got.Cost, err)
	}
	if want := 0.15 / hoursPerMonth; costPerGiBHour < want-1e-9 || costPerGiBHour > want+1e-9 {
		t.Errorf("cost = %v, want the flat premium_lrs rate %v", costPerGiBHour, want)
	}
}
