package test

// Relationship-type coverage (E2E KG test module, Tier A).
//
// Complements the per-provider NODE-type completeness tests (in the sources
// package) with an EDGE-type regression guard: it unions the relationship types
// across every committed scenario golden and asserts the set the Tier-A
// scenarios are expected to exercise is present. If a converter stops emitting
// one of these edge types, this fails.
//
// Broader per-provider edge inventories (AWS RUNS_AS/PROTECTS/ROUTES_THROUGH/
// ASSOCIATED_WITH/STORES_IN/ASSUMES/MANAGES, Azure ASSOCIATED_WITH/RUNS_AS/
// HAS_ACCESS_TO, GCP PULLS_FROM/USES_SECRET/SUBSCRIBES_TO, …) require meta-rich
// fixtures to trigger and are a documented follow-up; the goldens they'd feed
// live under testdata/e2e/<cloud>_all_types.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestE2E_RelationshipCoverage(t *testing.T) {
	goldenRoot := filepath.Join("testdata", "e2e")
	entries, err := os.ReadDir(goldenRoot)
	if err != nil {
		t.Fatalf("read golden root %s: %v", goldenRoot, err)
	}

	produced := map[string]bool{}
	scanned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(goldenRoot, e.Name(), "golden.json"))
		if err != nil {
			continue // scenarios without a committed golden (e.g. input-only dirs)
		}
		var g normGraph
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("parse golden %s: %v", e.Name(), err)
		}
		for _, ed := range g.Edges {
			produced[ed.RelationshipType] = true
		}
		scanned++
	}

	// Edge types the current Tier-A scenarios are expected to exercise.
	expected := []string{
		"RUNS_ON",
		"BELONGS_TO",
		"HOSTED_ON",
		"ROUTES_TO",
		"IS_ENCRYPTED_BY",
		"EMITS_LOGS_TO",
		"RESOLVES_TO",
		"PUBLISHES_TO",
		"CALLS",
	}
	var missing []string
	for _, rt := range expected {
		if !produced[rt] {
			missing = append(missing, rt)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("relationship coverage regressed; expected edge types missing from all %d goldens: %v", scanned, missing)
	}

	got := make([]string, 0, len(produced))
	for rt := range produced {
		got = append(got, rt)
	}
	sort.Strings(got)
	t.Logf("relationship coverage: %d distinct edge types across %d goldens: %v", len(produced), scanned, got)
}
