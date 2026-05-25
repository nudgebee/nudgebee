package integrations

import (
	"testing"

	"nudgebee/services/integrations/core"
)

// Verifies that a SNOW Table API record (with display_value-shaped reference
// fields and a JSON-string u_payload) is flattened into label keys the way
// downstream consumers expect.
func TestMergeServiceNowFieldsIntoLabels(t *testing.T) {
	record := map[string]any{
		// Plain scalars
		"number":            "INC0015515",
		"short_description": "MSSQL agent failed",
		"state":             "New",

		// Reference field shape
		"cmdb_ci": map[string]any{
			"value":         "abc123",
			"display_value": "rxtsqldev01",
			"link":          "https://example.service-now.com/api/now/table/cmdb_ci/abc123",
		},
		"business_service": map[string]any{
			"value":         "",
			"display_value": "Database Operations",
		},
		"caller_id": map[string]any{
			"value":         "user-123",
			"display_value": "Mohammad Farooque",
		},

		// Custom u_* string fields
		"u_cloud_technology":    "AWS",
		"u_emea_incident_id":    "EMEA-42",
		"u_environment":         "",
		"u_rxt_vendor_informed": "false",

		// u_payload is a JSON string; top-level scalars should be flattened
		"u_payload": `{"alarmName":"rmc-P3-EC2-disk","region":"eu-central-1","account":"248927988315"}`,

		// Numeric and bool
		"reopen_count": float64(3),
		"made_sla":     true,

		// Empty string should be skipped
		"sys_tags": "",
	}

	labels := map[string]string{}
	mergeServiceNowFieldsIntoLabels(record, labels)

	want := map[string]string{
		"number":                "INC0015515",
		"short_description":     "MSSQL agent failed",
		"state":                 "New",
		"cmdb_ci":               "rxtsqldev01",
		"cmdb_ci_value":         "abc123",
		"business_service":      "Database Operations",
		"caller_id":             "Mohammad Farooque",
		"caller_id_value":       "user-123",
		"u_cloud_technology":    "AWS",
		"u_emea_incident_id":    "EMEA-42",
		"u_rxt_vendor_informed": "false",
		"u_payload":             `{"alarmName":"rmc-P3-EC2-disk","region":"eu-central-1","account":"248927988315"}`,
		"payload.alarmName":     "rmc-P3-EC2-disk",
		"payload.region":        "eu-central-1",
		"payload.account":       "248927988315",
		"reopen_count":          "3",
		"made_sla":              "true",
	}

	for k, v := range want {
		if labels[k] != v {
			t.Errorf("labels[%q] = %q, want %q", k, labels[k], v)
		}
	}

	// Empty fields must not be present
	mustBeAbsent := []string{"u_environment", "sys_tags"}
	for _, k := range mustBeAbsent {
		if _, ok := labels[k]; ok {
			t.Errorf("labels[%q] should be absent (empty source value), got %q", k, labels[k])
		}
	}
}

// Confirms the subject is filled from cmdb_ci.display_value when the webhook
// path left it empty.
func TestApplyServiceNowSubjectFromRecord_PreferCmdbCi(t *testing.T) {
	record := map[string]any{
		"cmdb_ci": map[string]any{
			"display_value": "rxtsqldev01",
			"value":         "abc123",
		},
		"business_service": map[string]any{
			"display_value": "Database Ops",
		},
		"u_hostname": "should-not-win",
	}
	payload := &core.EventIncomingWebhook{}
	applyServiceNowSubjectFromRecord(record, payload)

	if payload.EventSubjectName != "rxtsqldev01" {
		t.Errorf("EventSubjectName = %q, want rxtsqldev01", payload.EventSubjectName)
	}
	if payload.EventSubjectKind != "service" {
		t.Errorf("EventSubjectKind = %q, want service", payload.EventSubjectKind)
	}
}

// Falls back to business_service when cmdb_ci is empty, then to u_hostname.
func TestApplyServiceNowSubjectFromRecord_FallbackChain(t *testing.T) {
	cases := []struct {
		name    string
		record  map[string]any
		wantSub string
	}{
		{
			name: "cmdb_ci empty -> business_service",
			record: map[string]any{
				"cmdb_ci":          map[string]any{"display_value": "", "value": ""},
				"business_service": map[string]any{"display_value": "DB Ops"},
				"u_hostname":       "host1",
			},
			wantSub: "DB Ops",
		},
		{
			name: "both refs empty -> u_hostname",
			record: map[string]any{
				"cmdb_ci":          map[string]any{"display_value": ""},
				"business_service": map[string]any{"display_value": ""},
				"u_hostname":       "host1",
			},
			wantSub: "host1",
		},
		{
			name:    "no candidates",
			record:  map[string]any{},
			wantSub: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := &core.EventIncomingWebhook{}
			applyServiceNowSubjectFromRecord(tc.record, payload)
			if payload.EventSubjectName != tc.wantSub {
				t.Errorf("subject = %q, want %q", payload.EventSubjectName, tc.wantSub)
			}
		})
	}
}

// Verifies an already-populated subject is never overwritten — important so
// the webhook's initial subject resolution from cmdb_ci_display isn't lost.
func TestApplyServiceNowSubjectFromRecord_DoesNotOverwrite(t *testing.T) {
	payload := &core.EventIncomingWebhook{
		EventSubjectName: "preset-host",
		EventSubjectKind: "host",
	}
	record := map[string]any{
		"cmdb_ci": map[string]any{"display_value": "should-not-win"},
	}
	applyServiceNowSubjectFromRecord(record, payload)
	if payload.EventSubjectName != "preset-host" {
		t.Errorf("EventSubjectName overwritten: got %q, want preset-host", payload.EventSubjectName)
	}
}

// incidentNumberFrom handles both string and reference-field shapes.
func TestIncidentNumberFrom(t *testing.T) {
	cases := []struct {
		name   string
		record map[string]any
		want   string
	}{
		{"string", map[string]any{"number": "INC0015515"}, "INC0015515"},
		{"ref display_value", map[string]any{"number": map[string]any{"display_value": "INC0015515", "value": "abc"}}, "INC0015515"},
		{"ref value only", map[string]any{"number": map[string]any{"value": "INC0015515"}}, "INC0015515"},
		{"missing", map[string]any{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := incidentNumberFrom(tc.record); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
