package agents

import (
	"encoding/json"
	"testing"
)

func TestShouldFailIncompleteFixRequest(t *testing.T) {
	cases := []struct {
		name       string
		incomplete bool
		mode       string
		req        NBAgentRequest
		want       bool
	}{
		{
			name:       "incomplete + fix + raise_pr fails",
			incomplete: true,
			mode:       ModeFix,
			req:        NBAgentRequest{RaisePR: true, Mode: ModeFix},
			want:       true,
		},
		{
			name:       "incomplete in explore mode does NOT fail (partial result OK for read-only)",
			incomplete: true,
			mode:       ModeExplore,
			req:        NBAgentRequest{Mode: ModeExplore},
			want:       false,
		},
		{
			name:       "incomplete + fix without raise_pr does NOT fail (no PR expected)",
			incomplete: true,
			mode:       ModeFix,
			req:        NBAgentRequest{Mode: ModeFix, RaisePR: false},
			want:       false,
		},
		{
			name:       "complete analysis never fails regardless of mode",
			incomplete: false,
			mode:       ModeFix,
			req:        NBAgentRequest{Mode: ModeFix, RaisePR: true},
			want:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldFailIncompleteFixRequest(tc.incomplete, tc.mode, tc.req); got != tc.want {
				t.Fatalf("shouldFailIncompleteFixRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectiveMode(t *testing.T) {
	cases := []struct {
		name string
		req  NBAgentRequest
		want string
	}{
		{
			name: "no mode, no raise_pr defaults to explore",
			req:  NBAgentRequest{},
			want: ModeExplore,
		},
		{
			name: "explicit explore wins over raise_pr=true",
			req:  NBAgentRequest{Mode: ModeExplore, RaisePR: true},
			want: ModeExplore,
		},
		{
			name: "explicit fix sets fix",
			req:  NBAgentRequest{Mode: ModeFix},
			want: ModeFix,
		},
		{
			name: "back-compat: raise_pr=true with no mode means fix",
			req:  NBAgentRequest{RaisePR: true},
			want: ModeFix,
		},
		{
			name: "unknown mode string falls back to raise_pr derivation",
			req:  NBAgentRequest{Mode: "weird", RaisePR: true},
			want: ModeFix,
		},
		{
			name: "unknown mode string with no raise_pr is explore",
			req:  NBAgentRequest{Mode: "weird"},
			want: ModeExplore,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.req.EffectiveMode(); got != tc.want {
				t.Fatalf("EffectiveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSanitizeExploreResponse_StripsFixOnlyFields(t *testing.T) {
	// Simulates a specialist that opportunistically populated fix-shaped fields
	// for what was actually an exploration question (the PR #29338 scenario).
	in := map[string]any{
		"title":               "Default postgres connection limit",
		"description":         "The Bitnami postgres chart defaults max_connections to 100.",
		"file_path":           "deploy/kubernetes/nudgebee/values.yaml",
		"line_number":         90,
		"original_code":       "postgresql:\n  enabled: true",
		"code_context":        "around the postgres block",
		"commits":             []any{map[string]any{"hash": "abc123"}},
		"pr_list":             []any{},
		"confidence_score":    "High",
		"investigation_trail": []any{"step1", "step2"},
		"root_cause_analysis": "n/a",
		"affected_components": []any{"postgres"},
		"semantic_analysis":   map[string]any{},

		// Fix-only — must be stripped:
		"requires_fix":                true,
		"fixed_code":                  "max_connections = 500",
		"git_diff":                    "diff --git a/...",
		"implementation_instructions": []any{map[string]any{"step": 1}},
		"alternative_fixes":           []any{},
		"execution_status":            "success",
		"execution_summary":           "applied",
		"files_modified":              []any{"values.yaml"},
		"verification_passed":         false,
		"verification_details":        "helm lint failed",
		"pr_info":                     map[string]any{"url": "..."},
		"automated_fix_pr_info":       map[string]any{"url": "https://github.com/..."},
		"fix_pr":                      map[string]any{"url": "..."},
		"pr_creation_status":          "success",
		"pr_creation_reason":          "ok",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	out := sanitizeExploreResponse(string(raw))

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	for _, k := range fixOnlyResponseFields {
		if _, present := got[k]; present {
			t.Errorf("expected fix-only field %q to be stripped, but it was present", k)
		}
	}

	mustKeep := []string{
		"title", "description", "file_path", "line_number",
		"original_code", "code_context", "commits", "pr_list",
		"confidence_score", "investigation_trail", "root_cause_analysis",
		"affected_components", "semantic_analysis",
	}
	for _, k := range mustKeep {
		if _, present := got[k]; !present {
			t.Errorf("expected explorer field %q to be preserved, but it was missing", k)
		}
	}

	if got["mode"] != ModeExplore {
		t.Errorf("expected mode=%q on sanitized response, got %v", ModeExplore, got["mode"])
	}
}

func TestSanitizeExploreResponse_PassesThroughInvalidJSON(t *testing.T) {
	// Malformed input must not crash and must not silently inject fields.
	bad := "not valid json {{{"
	if got := sanitizeExploreResponse(bad); got != bad {
		t.Fatalf("expected unchanged passthrough on invalid JSON, got %q", got)
	}
}
