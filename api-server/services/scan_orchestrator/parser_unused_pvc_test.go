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
	recs, err := ParseUnusedPVs(unused, ScanAccount{AccountID: "acc-1", TenantID: "tenant-1"})
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
	wantSavings := 50.0 * UnusedPVCSavingFactor
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
}

func TestParseUnusedPVs_DedupesByAccountObjectID(t *testing.T) {
	// Same PV appearing twice in the input list (e.g. Released phase + also
	// claimed by a no-pod PVC entry — degenerate but possible) shouldn't
	// produce duplicate recommendation rows because the conflict tuple
	// collapses them anyway.
	twin := makePV("pv-twin", "Released", "8Gi", "", "")
	recs, err := ParseUnusedPVs([]map[string]any{twin, twin}, ScanAccount{})
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
