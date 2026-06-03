package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Pure-logic unit tests for agent relevance helpers.
// These tests do NOT require a live cluster or TEST_ACCOUNT.
// ---------------------------------------------------------------------------

func TestExtractResourceQueryTerms(t *testing.T) {
	tests := []struct {
		query    string
		wantAll  []string // terms that MUST be present
		wantNone []string // terms that MUST NOT be present
	}{
		{
			query:    "find pods for the llm-server app",
			wantAll:  []string{"llm-server", "llm"},
			wantNone: []string{"find", "for", "the", "pod", "pods", "app"},
		},
		{
			query:    "search for llm server deployment",
			wantAll:  []string{"llm", "deployment"},
			wantNone: []string{"search", "for", "server"}, // "server" is generic
		},
		{
			query:    "find all postgres instances across my cluster",
			wantAll:  []string{"postgres"},
			wantNone: []string{"find", "all", "instances", "across", "cluster"},
		},
		{
			query:    "",
			wantAll:  []string{},
			wantNone: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := extractResourceQueryTerms(tt.query)
			gotSet := make(map[string]bool, len(got))
			for _, g := range got {
				gotSet[g] = true
			}
			for _, want := range tt.wantAll {
				assert.True(t, gotSet[want], "expected term %q to be present in %v", want, got)
			}
			for _, notWant := range tt.wantNone {
				assert.False(t, gotSet[notWant], "expected term %q to be absent from %v", notWant, got)
			}
		})
	}
}

func TestResourceNameMatchesTerms(t *testing.T) {
	tests := []struct {
		name     string
		terms    []string
		expected bool
	}{
		{"llm-server-abc123", []string{"llm", "llm-server"}, true},
		{"system:controller:resourcequota-controller", []string{"llm", "llm-server"}, false},
		{"system:resource-tracker", []string{"llm", "llm-server"}, false},
		{"postgres-primary-0", []string{"postgres"}, true},
		{"my-api-server", []string{"llm", "llm-server"}, false},
		{"", []string{"llm"}, false},
		{"anything", []string{}, false},
		// Cloud results that were incorrectly returned for "llm-server"
		{"lipi-games-resources-mobile-application-public", []string{"llm", "llm-server"}, false},
		{"resource-observer-scheduler", []string{"llm", "llm-server"}, false},
		{"gcp_billing_export_resource_v1_01766B", []string{"llm", "llm-server"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceNameMatchesTerms(tt.name, tt.terms)
			assert.Equal(t, tt.expected, got)
		})
	}
}
