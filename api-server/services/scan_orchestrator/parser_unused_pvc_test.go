package scan_orchestrator

import (
	"encoding/json"
	"testing"
)

func makePod(ns, name string, claims ...string) map[string]any {
	vols := make([]any, 0, len(claims))
	for _, c := range claims {
		vols = append(vols, map[string]any{
			"persistent_volume_claim": map[string]any{
				"claim_name": c,
			},
		})
	}
	return map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"volumes": vols},
	}
}

func makePVC(ns, name, phase string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"namespace": ns, "name": name},
		"status":   map[string]any{"phase": phase},
	}
}

func makePV(name, phase, capacity, claimRefNS, claimRefName string) map[string]any {
	pv := map[string]any{
		"metadata": map[string]any{"name": name, "namespace": ""},
		"spec":     map[string]any{"capacity": map[string]any{"storage": capacity}},
		"status":   map[string]any{"phase": phase},
	}
	if claimRefName != "" {
		pv["spec"].(map[string]any)["claim_ref"] = map[string]any{
			"namespace": claimRefNS, "name": claimRefName,
		}
	}
	return pv
}

func TestIdentifyUnusedPVs(t *testing.T) {
	pods := []map[string]any{
		makePod("demo", "app-1", "in-use-pvc"),
		makePod("demo", "app-2"), // no PVCs claimed
	}
	pvcs := []map[string]any{
		makePVC("demo", "in-use-pvc", "Bound"),
		makePVC("demo", "abandoned-pvc", "Bound"), // bound but no pod uses it → unused
		makePVC("demo", "pending-pvc", "Pending"), // not bound → ignored
	}
	pvs := []map[string]any{
		makePV("pv-bound-in-use", "Bound", "50Gi", "demo", "in-use-pvc"),
		makePV("pv-bound-abandoned", "Bound", "100Gi", "demo", "abandoned-pvc"),
		makePV("pv-released", "Released", "20Gi", "", ""),
		makePV("pv-available", "Available", "10Gi", "", ""),
		makePV("pv-other-bound", "Bound", "30Gi", "default", "something-else"), // bound to PVC outside the pvc list → not unused (PVC list is incomplete view)
	}

	unused := IdentifyUnusedPVs(pods, pvcs, pvs)
	names := map[string]bool{}
	for _, pv := range unused {
		md, _ := pv["metadata"].(map[string]any)
		name, _ := md["name"].(string)
		names[name] = true
	}

	want := []string{"pv-released", "pv-available", "pv-bound-abandoned"}
	for _, w := range want {
		if !names[w] {
			t.Errorf("expected %s in unused PV list", w)
		}
	}
	for _, dontWant := range []string{"pv-bound-in-use", "pv-other-bound"} {
		if names[dontWant] {
			t.Errorf("did NOT expect %s in unused PV list", dontWant)
		}
	}
	if got, expectAtLeast := len(unused), 3; got < expectAtLeast {
		t.Errorf("expected ≥%d unused PVs, got %d", expectAtLeast, got)
	}
}

func TestIdentifyUnusedPVs_CamelCaseFallback(t *testing.T) {
	// Older agent builds without SnakeKeysDeep would send camelCase keys.
	// We tolerate that for the persistent_volume_claim / claimRef fields.
	pods := []map[string]any{
		{
			"metadata": map[string]any{"namespace": "demo", "name": "app"},
			"spec": map[string]any{"volumes": []any{
				map[string]any{
					"persistentVolumeClaim": map[string]any{"claimName": "alive"},
				},
			}},
		},
	}
	pvcs := []map[string]any{
		makePVC("demo", "alive", "Bound"),
	}
	pvs := []map[string]any{
		// claimRef camelCase — should still be recognised
		{
			"metadata": map[string]any{"name": "pv-alive"},
			"status":   map[string]any{"phase": "Bound"},
			"spec": map[string]any{
				"capacity":  map[string]any{"storage": "10Gi"},
				"claimRef":  map[string]any{"namespace": "demo", "name": "alive"},
				"node_name": "",
			},
		},
	}
	unused := IdentifyUnusedPVs(pods, pvcs, pvs)
	if len(unused) != 0 {
		t.Fatalf("camelCase pod-volume should be recognised → pv-alive shouldn't be flagged. unused=%d", len(unused))
	}
}

func TestParseUnusedPVs_Shape(t *testing.T) {
	unused := []map[string]any{
		makePV("pv-released", "Released", "50Gi", "", ""),
	}
	recs, err := ParseUnusedPVs(unused, nil, "", ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec, got %d", len(recs))
	}
	r := recs[0]
	if r.RuleName != UnusedPVCRuleName {
		t.Errorf("rule_name = %q; want %q", r.RuleName, UnusedPVCRuleName)
	}
	if r.Category != "RightSizing" {
		t.Errorf("category = %q; want RightSizing", r.Category)
	}
	if r.Severity != "High" {
		t.Errorf("severity = %q; want High", r.Severity)
	}
	if r.AccountObjectID != "/pv-released" {
		t.Errorf("account_object_id = %q; want /pv-released (PVs are cluster-scoped → leading slash matches collector)", r.AccountObjectID)
	}
	// No storage class / provider signal on the PV → full monthly cost at
	// the fallback rate.
	wantSavings := 50.0 * fallbackStorageRatePerGBMonth
	if r.EstimatedSavings != wantSavings {
		t.Errorf("savings = %f; want %f", r.EstimatedSavings, wantSavings)
	}
	// Recommendation body should round-trip the PV JSON
	var body map[string]any
	if err := json.Unmarshal([]byte(r.Recommendation), &body); err != nil {
		t.Fatalf("recommendation not valid JSON: %v", err)
	}
	if md, _ := body["metadata"].(map[string]any); md["name"] != "pv-released" {
		t.Errorf("recommendation body lost metadata.name: %v", body)
	}
	pricing, _ := body["pricing"].(map[string]any)
	if pricing == nil || pricing["source"] != "fallback" {
		t.Errorf("recommendation body should carry pricing with source=fallback: %v", body["pricing"])
	}
}

func TestParseUnusedPVs_StorageClassRate(t *testing.T) {
	pv := makePV("pv-gcp-standard", "Released", "100Gi", "", "")
	pv["spec"].(map[string]any)["storage_class_name"] = "slow-disks"
	storageClasses := map[string]map[string]any{
		"slow-disks": {
			"metadata":    map[string]any{"name": "slow-disks"},
			"provisioner": "pd.csi.storage.gke.io",
			"parameters":  map[string]any{"type": "pd-standard"},
		},
	}
	recs, err := ParseUnusedPVs([]map[string]any{pv}, storageClasses, "", ScanAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec, got %d", len(recs))
	}
	if want := 100.0 * 0.04; recs[0].EstimatedSavings != want {
		t.Errorf("savings = %f; want %f (100GB pd-standard full monthly cost)", recs[0].EstimatedSavings, want)
	}
}

func TestStorageRateMapsConsistent(t *testing.T) {
	// Every disk type the resolution maps can emit must have a rate — a
	// drifted entry would otherwise silently price at the fallback. Guards
	// future rate edits; the python copies rely on the same invariant.
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

func TestResolveStoragePricing_Ladder(t *testing.T) {
	gkeClasses := map[string]map[string]any{
		"custom-ssd": {
			"provisioner": "pd.csi.storage.gke.io",
			"parameters":  map[string]any{"type": "pd-ssd"},
		},
		"standard": {
			"provisioner": "kubernetes.io/gce-pd",
		},
	}
	pvWithClass := func(class string) map[string]any {
		return map[string]any{"spec": map[string]any{"storage_class_name": class}}
	}

	if p := resolveStoragePricing(pvWithClass("custom-ssd"), gkeClasses, ""); p.Source != "parameters" || p.PricePerGB != 0.17 {
		t.Errorf("parameters rung: got %+v", p)
	}
	// Class without parameters → well-known GKE name.
	if p := resolveStoragePricing(pvWithClass("standard"), gkeClasses, ""); p.Source != "class_name" || p.PricePerGB != 0.04 {
		t.Errorf("class_name rung: got %+v", p)
	}
	// Unknown class, provider from the PV's own CSI driver → provider default.
	pvCSI := map[string]any{"spec": map[string]any{
		"storage_class_name": "who-knows",
		"csi":                map[string]any{"driver": "ebs.csi.aws.com"},
	}}
	if p := resolveStoragePricing(pvCSI, nil, ""); p.Source != "provider_default" || p.DiskType != "gp2" {
		t.Errorf("provider_default rung: got %+v", p)
	}
	// Azure skuName casing normalizes.
	azClasses := map[string]map[string]any{
		"fast": {
			"provisioner": "disk.csi.azure.com",
			"parameters":  map[string]any{"skuName": "Premium_LRS"},
		},
	}
	if p := resolveStoragePricing(pvWithClass("fast"), azClasses, ""); p.Source != "parameters" || p.PricePerGB != 0.12 {
		t.Errorf("azure skuName: got %+v", p)
	}
	// No PV-level signal at all → the account-provider backstop decides,
	// matching the python producers' get_k8s_provider rung.
	bare := map[string]any{"spec": map[string]any{}}
	if p := resolveStoragePricing(bare, nil, "azure"); p.Source != "provider_default" || p.PricePerGB != 0.075 {
		t.Errorf("account-provider backstop: got %+v", p)
	}
	// GKE's newer default class, and the only GCP type whose rate is not
	// shared with pd-balanced — a drifted entry here overstates savings on
	// most modern GKE clusters.
	hdClasses := map[string]map[string]any{
		"hyperdisk-balanced-rwo": {
			"provisioner": "pd.csi.storage.gke.io",
			"parameters":  map[string]any{"type": "hyperdisk-balanced"},
		},
	}
	if p := resolveStoragePricing(pvWithClass("hyperdisk-balanced-rwo"), hdClasses, ""); p.Source != "parameters" || p.PricePerGB != 0.08 {
		t.Errorf("hyperdisk-balanced: got %+v", p)
	}
	// On-prem class name that collides with a GKE default must NOT price as GCP.
	onPrem := map[string]map[string]any{
		"standard": {"provisioner": "rancher.io/local-path"},
	}
	if p := resolveStoragePricing(pvWithClass("standard"), onPrem, ""); p.Source != "fallback" || p.PricePerGB != fallbackStorageRatePerGBMonth {
		t.Errorf("on-prem fallback: got %+v", p)
	}
}

func TestParseUnusedPVs_DedupesByAccountObjectID(t *testing.T) {
	// Same PV appearing twice in the input list (e.g. Released phase + also
	// claimed by a no-pod PVC entry — degenerate but possible) shouldn't
	// produce duplicate recommendation rows because the conflict tuple
	// collapses them anyway.
	twin := makePV("pv-twin", "Released", "8Gi", "", "")
	recs, err := ParseUnusedPVs([]map[string]any{twin, twin}, nil, "", ScanAccount{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 rec (de-duped), got %d", len(recs))
	}
}

func TestParseSizeToGB(t *testing.T) {
	cases := map[string]float64{
		"50Gi":   50,
		"2048Mi": 2,
		"1Ti":    1024,
		"512Mi":  0.5,
		"":       0,
	}
	for in, want := range cases {
		got := parseSizeToGB(in)
		// allow tiny float drift on the divisions
		if diff := got - want; diff > 0.001 || diff < -0.001 {
			t.Errorf("parseSizeToGB(%q) = %f, want %f", in, got, want)
		}
	}
}
