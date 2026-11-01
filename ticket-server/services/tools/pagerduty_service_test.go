package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pagerduty "github.com/PagerDuty/go-pagerduty"
)

func TestNormalizePagerDutyUrgency(t *testing.T) {
	cases := map[string]string{
		"high":   "high",
		"High":   "high",
		"low":    "low",
		"Low":    "low",
		"0":      "low",
		"medium": "high", // PagerDuty has no medium — treated as high
		"":       "",     // empty defers to the service default
	}
	for in, want := range cases {
		if got := normalizePagerDutyUrgency(in); got != want {
			t.Errorf("normalizePagerDutyUrgency(%q) = %q, want %q", in, got, want)
		}
	}
}

const pdIncidentJSON = `{"incident": {
	"id": "Q1ABC",
	"title": "Checkout latency",
	"status": "triggered",
	"urgency": "high",
	"custom_fields": [{"name": "linked_jira", "display_name": "Linked Jira", "value": "OPS-42"}],
	"field_added_later": "kept"
}}`

func pdServer(t *testing.T, withCustomFields func(w http.ResponseWriter)) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Token token=pd-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("include[]") == "custom_fields" {
			withCustomFields(w)
			return
		}
		_, _ = w.Write([]byte(pdIncidentJSON))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestReadPagerDutyIncidentKeepsCustomFields(t *testing.T) {
	srv, seen := pdServer(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(pdIncidentJSON)) })
	client := pagerduty.NewClient("pd-test-token", pagerduty.WithAPIEndpoint(srv.URL))

	incident, full, err := readPagerDutyIncident(context.Background(), client, srv.URL, "Q1ABC")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got := strings.Join(*seen, ","); got != "/incidents/Q1ABC?include[]=custom_fields" {
		t.Errorf("requests = %s, want one read with the custom_fields include", got)
	}
	if incident.ID != "Q1ABC" || incident.Urgency != "high" {
		t.Errorf("typed incident = %+v", incident)
	}

	raw := pagerDutyRaw(incident, full)
	fields, _ := raw["custom_fields"].([]any)
	if len(fields) != 1 || fields[0].(map[string]any)["value"] != "OPS-42" {
		t.Errorf("raw custom_fields = %v, want the linked Jira value", raw["custom_fields"])
	}
	if raw["field_added_later"] != "kept" {
		t.Error("a key the SDK does not model must reach raw")
	}
	if raw["title"] != "Checkout latency" || raw["urgency"] != "high" {
		t.Errorf("keys the typed incident carried before must stay in raw, got %v", raw)
	}
}

func TestReadPagerDutyIncidentFallsBackWhenCustomFieldsAreRejected(t *testing.T) {
	srv, seen := pdServer(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error": {"message": "Invalid Input Provided"}}`))
	})
	client := pagerduty.NewClient("pd-test-token", pagerduty.WithAPIEndpoint(srv.URL))

	incident, full, err := readPagerDutyIncident(context.Background(), client, srv.URL, "Q1ABC")
	if err != nil {
		t.Fatalf("fallback read failed: %v", err)
	}
	if len(*seen) != 2 {
		t.Errorf("requests = %v, want the widened read then the plain one", *seen)
	}
	if incident.ID != "Q1ABC" || full != nil {
		t.Errorf("incident = %+v, full = %v; want the SDK read and no full body", incident, full)
	}
	if _, ok := pagerDutyRaw(incident, full)["title"]; !ok {
		t.Error("raw must still carry the typed incident after a fallback")
	}
}

func TestReadPagerDutyIncidentDoesNotRetryWhatCannotSucceed(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		srv, seen := pdServer(t, func(w http.ResponseWriter) { w.WriteHeader(status) })
		client := pagerduty.NewClient("pd-test-token", pagerduty.WithAPIEndpoint(srv.URL))

		_, _, err := readPagerDutyIncident(context.Background(), client, srv.URL, "Q1ABC")
		if err == nil || len(*seen) != 1 {
			t.Errorf("status %d: err = %v, requests = %v; want the error and no second request", status, err, *seen)
		}
	}

	srv, seen := pdServer(t, func(w http.ResponseWriter) { _, _ = w.Write([]byte(pdIncidentJSON)) })
	client := pagerduty.NewClient("pd-test-token", pagerduty.WithAPIEndpoint(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := readPagerDutyIncident(ctx, client, srv.URL, "Q1ABC"); err == nil || len(*seen) != 0 {
		t.Errorf("cancelled: err = %v, requests = %v; want the error and no request", err, *seen)
	}
}

// A deleted PagerDuty incident must be recognized as not-found via both read
// paths readPagerDutyIncident can return — Get() wraps this with
// ticket.ErrNotFound so the ticket-status sync job can skip it without
// counting toward its consecutive-failure abort guard.
func TestPagerDutyIsNotFound(t *testing.T) {
	notFoundSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(notFoundSrv.Close)
	client := pagerduty.NewClient("pd-test-token", pagerduty.WithAPIEndpoint(notFoundSrv.URL))

	// The custom-fields path's own status wrapper (pagerDutyStatusError).
	_, _, err := getPagerDutyIncidentWithCustomFields(context.Background(), client, notFoundSrv.URL, "Q1ABC")
	if !pagerDutyIsNotFound(err) {
		t.Errorf("getPagerDutyIncidentWithCustomFields 404: pagerDutyIsNotFound(%v) = false, want true", err)
	}

	// The SDK's own error type from the plain fallback read.
	_, err = client.GetIncidentWithContext(context.Background(), "Q1ABC")
	if !pagerDutyIsNotFound(err) {
		t.Errorf("GetIncidentWithContext 404: pagerDutyIsNotFound(%v) = false, want true", err)
	}

	if pagerDutyIsNotFound(errors.New("boom")) {
		t.Error("pagerDutyIsNotFound(generic error) = true, want false")
	}
}
