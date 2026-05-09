package triage

import (
	"encoding/json"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimeline(t *testing.T) {

	ctxt := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"), nil, nil, nil)
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctxt.GetLogger().Error("Failed to get database manager", "error", err)
		return
	}
	tenantID := ctxt.GetSecurityContext().GetTenantId()
	timeline, err := BuildEventTimeline(ctxt.GetContext(), dbms.Db, "b0897d37-2e81-4064-942e-bfae799532e0", tenantID)

	if err != nil {
		ctxt.GetLogger().Error("Failed to build event timeline", "error", err)
		return
	}
	// json print the timeline use printf
	jsonStr, err := json.Marshal(timeline)
	if err != nil {
		t.Fatalf("Error marshaling service map to JSON: %v", err)
	}
	t.Logf("Service Map: %s", jsonStr)
}

func TestExtractConfigChangeSummary(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		title    string
		expected string
	}{
		{
			name: "image and git change",
			evidence: `[{
				"type": "diff",
				"data": {
					"updated_paths": ["spec.template.spec.containers[0].image"],
					"updated_values": [
						{
							"path": "spec.template.spec.containers[0].image",
							"old": "registry.example.com/services:2025-12-08T05-52-46_5105d5a",
							"new": "registry.example.com/services:2025-12-08T08-22-55_2179cc3"
						}
					]
				}
			}]`,
			title:    "Deployment services-server updated",
			expected: "Image: 2025-12-08T05-52-46_5105d5a → 2025-12-08T08-22-55_2179cc3",
		},
		{
			name: "replica change",
			evidence: `[{
				"type": "diff",
				"data": {
					"updated_paths": ["spec.replicas"],
					"updated_values": [
						{
							"path": "spec.replicas",
							"old": 2,
							"new": 3
						}
					]
				}
			}]`,
			title:    "Deployment scaled",
			expected: "Replicas: 2 → 3",
		},
		{
			name:     "empty evidence",
			evidence: `[]`,
			title:    "Unknown change",
			expected: "Unknown change",
		},
		{
			name:     "invalid JSON",
			evidence: `invalid`,
			title:    "Fallback title",
			expected: "Fallback title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractConfigChangeSummary(json.RawMessage(tt.evidence), tt.title)
			if result != tt.expected {
				t.Errorf("extractConfigChangeSummary() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestDedupeTimelineEntries pins the fix for the duplicate-row bug observed in
// the Troubleshoot UI: an ImagePullBackOff event showed its deployment-config
// change as THREE separate rows (Config Change + Correlated Alert + Event)
// because getConfigChangeEntries, getCorrelatedEventEntries, and
// getSameResourceEventEntries each emit a row for the same underlying event.
// After dedup, each RefID appears exactly once, with the highest-priority
// Action (configuration_changed > alert_fired > related_resource_event).
func TestDedupeTimelineEntries(t *testing.T) {
	t1 := time.Date(2026, 4, 23, 23, 25, 57, 0, time.UTC)
	t2 := time.Date(2026, 4, 23, 23, 25, 56, 0, time.UTC)
	t3 := time.Date(2026, 4, 23, 23, 28, 47, 0, time.UTC)

	tests := []struct {
		name     string
		in       []TimelineEntry
		wantIDs  []string // deduped RefIDs in any order (dedupe doesn't sort)
		wantActs map[string]string
	}{
		{
			name: "config_change wins over correlated_alert and related_resource_event",
			// Reproduces the exact screenshot case: single config-change event
			// picked up by three parallel bucket queries.
			in: []TimelineEntry{
				{Timestamp: t1, RefType: "event", RefID: "deploy-1", Action: "related_resource_event", Summary: "deployment/x.yaml created"},
				{Timestamp: t1, RefType: "config_change", RefID: "deploy-1", Action: "configuration_changed", Summary: "deployment/x.yaml created"},
				{Timestamp: t1, RefType: "event", RefID: "deploy-1", Action: "alert_fired", Summary: "deployment/x.yaml created"},
			},
			wantIDs:  []string{"deploy-1"},
			wantActs: map[string]string{"deploy-1": "configuration_changed"},
		},
		{
			name: "alert_fired wins over related_resource_event",
			in: []TimelineEntry{
				{Timestamp: t2, RefType: "event", RefID: "pod-warn-1", Action: "related_resource_event", Summary: "Failed Warning"},
				{Timestamp: t2, RefType: "event", RefID: "pod-warn-1", Action: "alert_fired", Summary: "Failed Warning"},
			},
			wantIDs:  []string{"pod-warn-1"},
			wantActs: map[string]string{"pod-warn-1": "alert_fired"},
		},
		{
			name: "main event with fired + dedup of same-resource row",
			in: []TimelineEntry{
				{Timestamp: t3, RefType: "event", RefID: "main", Action: "fired", Summary: "Alert"},
				{Timestamp: t3, RefType: "event", RefID: "main", Action: "related_resource_event", Summary: "Alert"},
			},
			wantIDs:  []string{"main"},
			wantActs: map[string]string{"main": "fired"},
		},
		{
			name: "distinct RefIDs are all preserved",
			in: []TimelineEntry{
				{Timestamp: t1, RefType: "config_change", RefID: "deploy-1", Action: "configuration_changed"},
				{Timestamp: t2, RefType: "event", RefID: "pod-1", Action: "alert_fired"},
				{Timestamp: t3, RefType: "event", RefID: "main", Action: "fired"},
			},
			wantIDs: []string{"deploy-1", "pod-1", "main"},
			wantActs: map[string]string{
				"deploy-1": "configuration_changed",
				"pod-1":    "alert_fired",
				"main":     "fired",
			},
		},
		{
			name: "empty RefID rows pass through unchanged (no collapse)",
			in: []TimelineEntry{
				{Timestamp: t1, RefType: "note", RefID: "", Action: "annotation", Summary: "first note"},
				{Timestamp: t2, RefType: "note", RefID: "", Action: "annotation", Summary: "second note"},
				{Timestamp: t3, RefType: "event", RefID: "x", Action: "fired"},
			},
			// Two empty-RefID rows + one unique → 3 entries out
			wantIDs: nil, // assert via Len below
		},
		{
			name:    "single entry returns unchanged",
			in:      []TimelineEntry{{RefType: "event", RefID: "a", Action: "fired"}},
			wantIDs: []string{"a"},
			wantActs: map[string]string{
				"a": "fired",
			},
		},
		{
			name:    "empty input returns empty",
			in:      nil,
			wantIDs: nil,
		},
		{
			name: "unknown Action ranks below known ones (does not win)",
			in: []TimelineEntry{
				{RefType: "event", RefID: "x", Action: "alert_fired"},
				{RefType: "event", RefID: "x", Action: "some_new_action"},
			},
			wantIDs:  []string{"x"},
			wantActs: map[string]string{"x": "alert_fired"},
		},
	}

	// event_history entries share the main event's RefID but represent distinct
	// lifecycle occurrences (status change, comment, assignment, resolution).
	// They must all pass through dedup alongside the main 'fired' entry.
	// Asserted as a separate test below because it has a different shape
	// (multiple rows kept under the same RefID).
	t.Run("event_history entries preserved alongside fired entry", func(t *testing.T) {
		main := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)
		statusChange := main.Add(5 * time.Minute)
		assigned := main.Add(7 * time.Minute)
		commented := main.Add(10 * time.Minute)
		resolved := main.Add(15 * time.Minute)

		in := []TimelineEntry{
			{Timestamp: main, RefType: "event", RefID: "evt-1", Action: "fired", Summary: "Alert fired"},
			{Timestamp: statusChange, RefType: "event_history", RefID: "evt-1", Action: "status_changed", Summary: "open → investigating"},
			{Timestamp: assigned, RefType: "event_history", RefID: "evt-1", Action: "assigned", Summary: "assigned to oncall"},
			{Timestamp: commented, RefType: "event_history", RefID: "evt-1", Action: "commented", Summary: "investigating root cause"},
			{Timestamp: resolved, RefType: "event_history", RefID: "evt-1", Action: "status_changed", Summary: "investigating → resolved"},
			// And a same-resource generic row that SHOULD still collapse against the main event's 'fired'.
			{Timestamp: main, RefType: "event", RefID: "evt-1", Action: "related_resource_event", Summary: "Alert fired"},
		}

		got := dedupeTimelineEntries(in)
		// Expect: fired (1) + 4 history entries (4) = 5. The related_resource_event
		// row collapses against 'fired'.
		assert.Len(t, got, 5)

		// Collect Actions in order to confirm history is intact.
		actions := make([]string, 0, len(got))
		for _, e := range got {
			actions = append(actions, e.Action)
		}
		assert.Equal(t, []string{
			"fired",
			"status_changed",
			"assigned",
			"commented",
			"status_changed",
		}, actions, "every event_history row must survive dedup even though they share RefID with the main 'fired' row")
	})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupeTimelineEntries(tc.in)

			if tc.name == "empty RefID rows pass through unchanged (no collapse)" {
				assert.Len(t, got, 3)
				return
			}

			// Collect deduped RefIDs + their Actions
			gotIDs := make([]string, 0, len(got))
			gotActs := make(map[string]string, len(got))
			for _, e := range got {
				gotIDs = append(gotIDs, e.RefID)
				gotActs[e.RefID] = e.Action
			}
			assert.ElementsMatch(t, tc.wantIDs, gotIDs)
			for id, wantAction := range tc.wantActs {
				assert.Equal(t, wantAction, gotActs[id], "RefID=%s", id)
			}
		})
	}
}

func TestExtractImageTag(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "full image URL",
			input:    "registry.example.com/services:2025-12-08T05-52-46_5105d5a",
			expected: "2025-12-08T05-52-46_5105d5a",
		},
		{
			name:     "image with digest",
			input:    "registry.com/image:tag@sha256:abcd1234",
			expected: "tag",
		},
		{
			name:     "no tag",
			input:    "registry.com/image",
			expected: "registry.com/image",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "nil value",
			input:    nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractImageTag(tt.input)
			if result != tt.expected {
				t.Errorf("extractImageTag() = %v, want %v", result, tt.expected)
			}
		})
	}
}
