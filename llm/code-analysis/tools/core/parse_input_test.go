package core

import "testing"

type cloneArgs struct {
	RepoURL string  `json:"repo_url"`
	Branch  string  `json:"branch,omitempty"`
	Shallow bool    `json:"shallow,omitempty"`
	Depth   int     `json:"depth,omitempty"`
	Ratio   float64 `json:"ratio,omitempty"`
}

// Models routinely quote booleans and numbers in tool arguments. encoding/json
// rejects those outright, which failed the entire call with "Failed to parse
// tool input parameters" — and the planner's retry dropped arguments it had
// gotten right. A live repo_clone lost its `branch: "main"` exactly this way
// after `shallow: "true"` failed the first attempt.
func TestParseInputCoercesQuotedScalars(t *testing.T) {
	var got cloneArgs
	err := ParseInput(map[string]any{
		"repo_url": "https://github.com/nudgebee/nudgebee-infra.git",
		"branch":   "main",
		"shallow":  "true",
		"depth":    "1",
		"ratio":    "0.5",
	}, &got)
	if err != nil {
		t.Fatalf("quoted scalars should parse, got: %v", err)
	}
	if !got.Shallow {
		t.Error(`"shallow": "true" should coerce to true`)
	}
	if got.Depth != 1 {
		t.Errorf(`"depth": "1" should coerce to 1, got %d`, got.Depth)
	}
	if got.Ratio != 0.5 {
		t.Errorf(`"ratio": "0.5" should coerce to 0.5, got %v`, got.Ratio)
	}
	// The whole point: the other arguments survive rather than being retried away.
	if got.Branch != "main" {
		t.Errorf("branch should be preserved, got %q", got.Branch)
	}
}

// Coercion must not paper over arguments that are genuinely the wrong type —
// a real type error should still surface as one.
func TestParseInputLeavesUnparseableStringsAlone(t *testing.T) {
	var got cloneArgs
	if err := ParseInput(map[string]any{"repo_url": "x", "shallow": "yes please"}, &got); err == nil {
		t.Error("a string that is not a bool should still fail to parse")
	}
}

// Native types and absent fields keep working untouched.
func TestParseInputPassesThroughNativeTypes(t *testing.T) {
	var got cloneArgs
	if err := ParseInput(map[string]any{"repo_url": "x", "shallow": true, "depth": 3}, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Shallow || got.Depth != 3 {
		t.Errorf("native values should pass through unchanged, got %+v", got)
	}

	var empty cloneArgs
	if err := ParseInput(map[string]any{"repo_url": "x"}, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Shallow || empty.Depth != 0 {
		t.Errorf("absent fields should stay zero, got %+v", empty)
	}
}

// A non-struct target (map, pointer to map) must not trip the reflection walk.
func TestParseInputHandlesNonStructTarget(t *testing.T) {
	var got map[string]any
	if err := ParseInput(map[string]any{"shallow": "true"}, &got); err != nil {
		t.Fatal(err)
	}
	if got["shallow"] != "true" {
		t.Errorf("a map target should receive the raw value, got %v", got["shallow"])
	}
}
