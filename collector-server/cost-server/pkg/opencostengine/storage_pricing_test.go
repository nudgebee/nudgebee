package opencostengine

import (
	"strconv"
	"testing"
)

func TestResolveStorageRatePerGBMonth(t *testing.T) {
	cases := []struct {
		name       string
		className  string
		parameters map[string]string
		provider   string
		want       float64
	}{
		{"gcp parameters type", "custom-ssd", map[string]string{"type": "pd-ssd"}, "gcp", 0.17},
		{"azure skuName casing", "fast", map[string]string{"skuName": "Premium_LRS"}, "azure", 0.12},
		{"gcp hyperdisk balanced", "hyperdisk-balanced-rwo", map[string]string{"type": "hyperdisk-balanced"}, "gcp", 0.08},
		{"well-known gke standard", "standard", nil, "gcp", 0.04},
		{"well-known aks managed-premium", "managed-premium", nil, "azure", 0.12},
		{"unknown class provider default", "who-knows", nil, "aws", 0.10},
		{"unknown parameters provider default", "x", map[string]string{"type": "exotic"}, "aws", 0.10},
		{"no provider fallback", "standard", nil, "", fallbackStorageRatePerGBMonth},
		{"unsupported provider fallback", "standard", nil, "civo", fallbackStorageRatePerGBMonth},
	}
	for _, tc := range cases {
		if got := resolveStorageRatePerGBMonth(tc.className, tc.parameters, tc.provider); got != tc.want {
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
