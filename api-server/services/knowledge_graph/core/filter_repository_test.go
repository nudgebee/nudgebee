package core

import (
	"sort"
	"testing"
)

func TestExpandIfEmpty(t *testing.T) {
	universe := []string{"aws", "k8s", "gcp"}

	if got := expandIfEmpty(nil, universe); !equalUnordered(got, universe) {
		t.Errorf("expandIfEmpty(nil) = %v, want %v", got, universe)
	}
	if got := expandIfEmpty([]string{}, universe); !equalUnordered(got, universe) {
		t.Errorf("expandIfEmpty([]) = %v, want %v", got, universe)
	}
	sel := []string{"aws"}
	if got := expandIfEmpty(sel, universe); !equalUnordered(got, sel) {
		t.Errorf("expandIfEmpty(%v) = %v, want %v", sel, got, sel)
	}
}

// TestRemovedComputation locks in the empty(="all") expansion semantics that
// UpsertDefaultFilterForTenant relies on to decide what a save deactivated. The bug
// this guards against: every tenant's stored filter is empty(=all) by default (the
// cron pre-creates it), so a naive diff([], subset) returns nothing and deselected
// items are never soft-deleted.
func TestRemovedComputation(t *testing.T) {
	universe := []string{"datadog-apm", "ebpf", "newrelic-apm", "traces"}

	tests := []struct {
		name     string
		existing []string
		updated  []string
		want     []string
	}{
		{
			name:     "empty(all) -> subset removes the deselected ones",
			existing: []string{},
			updated:  []string{"ebpf", "traces", "newrelic-apm"},
			want:     []string{"datadog-apm"},
		},
		{
			name:     "explicit -> empty(all) removes nothing (re-enabling everything)",
			existing: []string{"ebpf"},
			updated:  []string{},
			want:     nil,
		},
		{
			name:     "empty(all) -> empty(all) removes nothing",
			existing: []string{},
			updated:  []string{},
			want:     nil,
		},
		{
			name:     "explicit full -> subset removes the deselected one",
			existing: []string{"datadog-apm", "ebpf", "newrelic-apm", "traces"},
			updated:  []string{"ebpf", "traces"},
			want:     []string{"datadog-apm", "newrelic-apm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringSliceDiff(
				expandIfEmpty(tt.existing, universe),
				expandIfEmpty(tt.updated, universe),
			)
			if !equalUnordered(got, tt.want) {
				t.Errorf("removed = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestToggleableFlowSourcesExcludesAlwaysOn guards the invariant that the universe we
// expand an empty flow_sources filter into never contains an always-on source like
// "manual" — otherwise a save could soft-delete manually declared dependencies.
func TestToggleableFlowSourcesExcludesAlwaysOn(t *testing.T) {
	for _, name := range alwaysOnFlowSources {
		for _, toggleable := range toggleableFlowSources {
			if toggleable == name {
				t.Errorf("always-on flow source %q must not be in toggleableFlowSources", name)
			}
		}
	}
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
