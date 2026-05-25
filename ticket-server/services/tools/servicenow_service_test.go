package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/tickets-server/models"

	"github.com/gin-gonic/gin"
)

func TestMarkdownToPlainText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain passthrough", "hello world", "hello world"},
		{"bold asterisks", "check the **docs** now", "check the docs now"},
		{"bold underscores", "see __readme__ first", "see readme first"},
		{"italic asterisks", "it is *urgent* today", "it is urgent today"},
		{"italic underscores", "word _emphasis_ here", "word emphasis here"},
		{"header", "# Root Cause\nPod OOM killed", "Root Cause\nPod OOM killed"},
		{"bullets", "- first\n- second", "• first\n• second"},
		{"link", "see [docs](https://x.y) please", "see docs (https://x.y) please"},
		{"image", "![logo](https://x/y.png) after", "logo after"},
		{"inline code", "run `kubectl get pods` now", "run kubectl get pods now"},
		{
			"code fence",
			"before\n```go\nfmt.Println(\"hi\")\n```\nafter",
			"before\nfmt.Println(\"hi\")\n\nafter",
		},
		{"horizontal rule", "above\n---\nbelow", "above\n\nbelow"},
		{"collapse blank lines", "a\n\n\n\nb", "a\n\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := markdownToPlainText(tc.in)
			if got != tc.want {
				t.Errorf("markdownToPlainText(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Verifies that ServiceNowService.Get fetches a single record via the Table API
// using sysparm_display_value=all, populates the typed Ticket fields by
// preferring display_value for human-facing fields and value for state/urgency
// codes, and stores the full record in Ticket.Raw including u_* custom fields
// and reference fields in {value, display_value} shape.
func TestServiceNowService_Get_PopulatesRawWithAllFields(t *testing.T) {
	// Stub SNOW Table API
	var capturedQuery string
	var capturedAuth string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "result": [
		    {
		      "sys_id":            "abc123",
		      "number":            "INC0010072",
		      "short_description": "[NB-TEST] enrichment",
		      "description":       "verifying raw passthrough",
		      "state":             {"value": "1", "display_value": "New"},
		      "urgency":           {"value": "2", "display_value": "2 - Medium"},
		      "sys_created_on":    "2026-05-04 18:29:04",
		      "cmdb_ci":           {"value": "ci-sys-id", "display_value": "Storage Area Network 001"},
		      "business_service":  {"value": "bs-sys-id", "display_value": "Email"},
		      "u_cloud_technology": "AWS",
		      "u_emea_incident_id": "EMEA-42",
		      "u_environment":      ""
		    }
		  ]
		}`))
	}))
	defer stub.Close()

	svc := &ServiceNowService{}
	cfg := models.TicketConfigurations{
		URL:      stub.URL, // bare URL — Get must accept http:// without forcing https://
		Username: "alice",
		Password: "secret",
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	got, err := svc.Get(ctx, cfg, "INC0010072")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	// Authorization must be HTTP Basic with the configured creds.
	if !strings.HasPrefix(capturedAuth, "Basic ") {
		t.Errorf("Authorization = %q, want Basic prefix", capturedAuth)
	}

	// Query must request all display values, exclude reference links, and
	// match by number — this is what gives us cmdb_ci/business_service/u_*.
	for _, want := range []string{
		"sysparm_display_value=all",
		"sysparm_exclude_reference_link=true",
		"sysparm_query=number%3DINC0010072",
		"sysparm_limit=1",
	} {
		if !strings.Contains(capturedQuery, want) {
			t.Errorf("query missing %q; full=%s", want, capturedQuery)
		}
	}

	// Typed fields: short_description and number passthrough; state/urgency
	// must use the raw value side so existing mapping logic (1 -> New,
	// 2 -> nudgebee priority) keeps working.
	if got.TicketID != "INC0010072" {
		t.Errorf("TicketID = %q, want INC0010072", got.TicketID)
	}
	if got.Title != "[NB-TEST] enrichment" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Status != "New" {
		t.Errorf("Status = %q, want New (mapped from state.value=1)", got.Status)
	}
	if got.Platform != "servicenow" {
		t.Errorf("Platform = %q", got.Platform)
	}

	// Raw must carry every field from the API verbatim, including the
	// reference-shape (cmdb_ci / business_service) and u_* customs.
	if got.Raw == nil {
		t.Fatal("Raw is nil; expected full record")
	}
	if cmdb, ok := got.Raw["cmdb_ci"].(map[string]any); !ok {
		t.Errorf("Raw.cmdb_ci is not a map: %T", got.Raw["cmdb_ci"])
	} else if cmdb["display_value"] != "Storage Area Network 001" {
		t.Errorf("Raw.cmdb_ci.display_value = %v", cmdb["display_value"])
	}
	if bs, ok := got.Raw["business_service"].(map[string]any); !ok || bs["display_value"] != "Email" {
		t.Errorf("Raw.business_service shape unexpected: %v", got.Raw["business_service"])
	}
	if got.Raw["u_cloud_technology"] != "AWS" {
		t.Errorf("Raw.u_cloud_technology = %v", got.Raw["u_cloud_technology"])
	}
	if _, ok := got.Raw["u_emea_incident_id"]; !ok {
		t.Errorf("Raw missing u_emea_incident_id; got keys: %v", mapKeys(got.Raw))
	}

	// JSON round-trip: Raw must serialize to the response body so the
	// runbook-server side can mapstructure-decode it.
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"raw"`) {
		t.Errorf("Marshalled JSON missing \"raw\" key: %s", string(out))
	}
}

// Verifies that ticket IDs containing SNOW query operators are rejected
// before composing the request. Without this guard, "INC1^statefired" or
// similar would let an attacker rewrite the WHERE clause and exfiltrate
// records they shouldn't see.
func TestServiceNowService_Get_RejectsQueryInjection(t *testing.T) {
	// Hit count must stay zero — request should never reach SNOW.
	var hits int
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":[]}`))
	}))
	defer stub.Close()

	svc := &ServiceNowService{}
	cfg := models.TicketConfigurations{URL: stub.URL, Username: "u", Password: "p"}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	for _, malicious := range []string{
		`INC0010072^state=resolved`,
		`INC0010072^ORnumberSTARTSWITHINC`,
		`INC0010072!=`,
		`anything<5`,
	} {
		t.Run(malicious, func(t *testing.T) {
			_, err := svc.Get(ctx, cfg, malicious)
			if err == nil {
				t.Errorf("expected error for malicious ticket id %q, got nil", malicious)
			}
			if !strings.Contains(err.Error(), "invalid ticket ID") {
				t.Errorf("error = %v, want 'invalid ticket ID' wrapping", err)
			}
		})
	}

	if hits != 0 {
		t.Errorf("malicious id reached SNOW (hits=%d); sanitizer must short-circuit before request", hits)
	}
}

// Verifies that Get returns a clear error when the SNOW Table API responds
// with no matching record (empty result array). Workflows depend on this
// for graceful degradation when a referenced ticket has been deleted.
func TestServiceNowService_Get_NotFound(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result": []}`))
	}))
	defer stub.Close()

	svc := &ServiceNowService{}
	cfg := models.TicketConfigurations{URL: stub.URL, Username: "u", Password: "p"}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	_, err := svc.Get(ctx, cfg, "INC9999999")
	if err == nil || !strings.Contains(err.Error(), "incident not found") {
		t.Errorf("expected 'incident not found' error, got %v", err)
	}
}

// Verifies refOrString prefers display_value for reference-shape fields
// while falling back to value when display_value is empty, and passes
// scalar strings through unchanged.
func TestRefOrString(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"plain string", "INC0010072", "INC0010072"},
		{"ref with display_value", map[string]any{"value": "abc", "display_value": "Storage SAN"}, "Storage SAN"},
		{"ref with empty display, falls to value", map[string]any{"value": "fallback", "display_value": ""}, "fallback"},
		{"ref with only value", map[string]any{"value": "raw-only"}, "raw-only"},
		{"nil", nil, ""},
		{"unexpected type (number)", float64(42), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refOrString(tc.in); got != tc.want {
				t.Errorf("refOrString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Verifies refValue returns the database value side regardless of shape —
// callers (state, urgency mappers) rely on raw codes like "1" not "New".
func TestRefValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"plain string", "1", "1"},
		{"ref shape", map[string]any{"value": "1", "display_value": "New"}, "1"},
		{"ref with no value", map[string]any{"display_value": "New"}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refValue(tc.in); got != tc.want {
				t.Errorf("refValue(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
