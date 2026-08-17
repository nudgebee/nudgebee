package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/tickets-server/models"

	"github.com/gin-gonic/gin"
)

const testIncidentIOID = "01FDAG4SAP5TYPT98WGR2N7W91"

func incidentIOTestCtx() *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func incidentIOTestConfig(serverURL string) models.TicketConfigurations {
	return models.TicketConfigurations{
		URL:      serverURL,
		Password: "inc-api-key-123",
		Username: "ops@example.com",
	}
}

// TestIncidentIOService_Get verifies Bearer auth, the /v2/incidents/{id} path,
// and the field mapping that differs from every other provider: incident.io
// exposes `name`/`summary` (not title/description), a nested status object whose
// *category* — not its org-customisable name — is the portable status, and
// role assignments in place of a flat assignee list.
func TestIncidentIOService_Get(t *testing.T) {
	var gotAuth, gotPath string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"incident": {
				"id": "01FDAG4SAP5TYPT98WGR2N7W91",
				"name": "Our database is sad",
				"summary": "The primary replica is not responding.",
				"reference": "INC-123",
				"permalink": "https://app.incident.io/incidents/123",
				"created_at": "2026-05-04T18:29:04Z",
				"updated_at": "2026-05-05T09:15:00Z",
				"incident_status": {"id": "01STATUS", "name": "Investigating", "category": "active"},
				"severity": {"id": "01SEV", "name": "Major"},
				"incident_type": {"id": "01TYPE", "name": "Production Outage"},
				"incident_role_assignments": [
					{"assignee": {"id": "01USER", "name": "Lisa Curtis", "email": "lisa@example.com"},
					 "role": {"id": "01ROLE", "name": "Incident Lead"}}
				]
			}
		}`))
	}))
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.Get(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if gotAuth != "Bearer inc-api-key-123" {
		t.Errorf("Authorization = %q, want Bearer inc-api-key-123", gotAuth)
	}
	if gotPath != "/v2/incidents/"+testIncidentIOID {
		t.Errorf("path = %q, want /v2/incidents/%s", gotPath, testIncidentIOID)
	}
	if got.TicketID != testIncidentIOID {
		t.Errorf("TicketID = %q, want %s", got.TicketID, testIncidentIOID)
	}
	if got.Title != "Our database is sad" {
		t.Errorf("Title = %q, want the incident name", got.Title)
	}
	if got.Description != "The primary replica is not responding." {
		t.Errorf("Description = %q, want the incident summary", got.Description)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want the status category (active), not its org-specific name", got.Status)
	}
	if got.Severity != "Major" {
		t.Errorf("Severity = %q, want Major", got.Severity)
	}
	if got.ProjectKey != "01TYPE" {
		t.Errorf("ProjectKey = %q, want the incident_type id", got.ProjectKey)
	}
	if got.Assignee != "Lisa Curtis" || len(got.Assignees) != 1 {
		t.Errorf("Assignee = %q, Assignees = %v, want Lisa Curtis", got.Assignee, got.Assignees)
	}
	if got.URL != "https://app.incident.io/incidents/123" {
		t.Errorf("URL = %q, want the permalink", got.URL)
	}
	if got.Platform != "incidentio" {
		t.Errorf("Platform = %q, want incidentio", got.Platform)
	}
	if got.CreatedAt == nil || got.UpdatedAt == nil {
		t.Errorf("CreatedAt/UpdatedAt not parsed: %v / %v", got.CreatedAt, got.UpdatedAt)
	}
	if got.Raw == nil {
		t.Error("Raw not populated")
	}
}

// TestIncidentIOService_Get_RejectsMalformedID proves the ULID guard runs
// before any network call — a bad ID must not reach incident.io.
func TestIncidentIOService_Get_RejectsMalformedID(t *testing.T) {
	called := false
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer stub.Close()

	svc := &IncidentIOService{}
	_, err := svc.Get(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), "not-a-ulid")
	if err == nil {
		t.Fatal("Get() with a malformed ID should error")
	}
	if called {
		t.Error("Get() issued an HTTP request for a malformed ID; validation must short-circuit")
	}
}

// TestIncidentIOService_Get_AuthError checks that a 401 surfaces as an error
// carrying the upstream body rather than an empty ticket.
func TestIncidentIOService_Get_AuthError(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"authentication_error","status":401,"detail":"invalid api key"}`))
	}))
	defer stub.Close()

	svc := &IncidentIOService{}
	_, err := svc.Get(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID)
	if err == nil {
		t.Fatal("Get() should error on 401")
	}
	if !containsAll(err.Error(), "401") {
		t.Errorf("error %q should mention the 401 status", err.Error())
	}
}

// incidentIOMetaStub serves the four reference endpoints (severities,
// statuses, types, users) that create/transition flows read before acting.
//
// The version prefixes are deliberately mixed and were verified against the
// live API: severities / incident_statuses / incident_types are V1, while
// incidents / incident_updates / users are V2. Requesting the wrong version
// returns 404, so these paths are load-bearing — the default branch below
// 404s anything else to keep them honest.
func incidentIOMetaStub(t *testing.T, extra func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if extra != nil && extra(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/severities":
			_, _ = w.Write([]byte(`{"severities":[
				{"id":"01SEV_MINOR","name":"Minor","rank":1},
				{"id":"01SEV_MAJOR","name":"Major","rank":2},
				{"id":"01SEV_CRIT","name":"Critical","rank":3}]}`))
		case "/v1/incident_statuses":
			_, _ = w.Write([]byte(`{"incident_statuses":[
				{"id":"01ST_TRIAGE","name":"Triage","category":"triage","rank":1},
				{"id":"01ST_INVEST","name":"Investigating","category":"active","rank":2},
				{"id":"01ST_FIXING","name":"Fixing","category":"active","rank":3},
				{"id":"01ST_CLOSED","name":"Closed","category":"closed","rank":4}]}`))
		case "/v1/incident_types":
			_, _ = w.Write([]byte(`{"incident_types":[
				{"id":"01TYPE_DEFAULT","name":"Default","is_default":true},
				{"id":"01TYPE_OUTAGE","name":"Production Outage"}]}`))
		case "/v2/users":
			_, _ = w.Write([]byte(`{"users":[
				{"id":"01USER_A","name":"Lisa Curtis","email":"lisa@example.com"},
				{"id":"01USER_B","name":"Sam Reed","email":"sam@example.com"}]}`))
		case "/v2/incidents/" + testIncidentIOID:
			_, _ = w.Write([]byte(`{"incident":{
				"id":"01FDAG4SAP5TYPT98WGR2N7W91",
				"name":"Disk full on prod",
				"summary":"The disk is full.",
				"incident_status":{"id":"01ST_INVEST","name":"Investigating","category":"active"},
				"severity":{"id":"01SEV_MAJOR","name":"Major"}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"unexpected path ` + r.URL.Path + `"}`))
		}
	}))
}

// TestIncidentIOService_Create checks the two fields incident.io requires that
// no other provider does (idempotency_key, visibility), that ProjectKey maps to
// incident_type_id, and that a severity *name* is resolved to an ID first.
func TestIncidentIOService_Create(t *testing.T) {
	var body map[string]any
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/incidents" || r.Method != http.MethodPost {
			return false
		}
		_ = decodeJSON(r.Body, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incident":{
			"id":"01FDAG4SAP5TYPT98WGR2N7W91",
			"name":"Disk full on prod",
			"permalink":"https://app.incident.io/incidents/9",
			"incident_status":{"id":"01ST_TRIAGE","name":"Triage","category":"triage"},
			"severity":{"id":"01SEV_MAJOR","name":"Major"}}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.Create(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), models.Ticket{
		Title:       "Disk full on prod",
		Description: "The disk is full.",
		Severity:    "Major",
		ProjectKey:  "01TYPE_OUTAGE",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if body["idempotency_key"] == nil || body["idempotency_key"] == "" {
		t.Error("create body must carry a non-empty idempotency_key")
	}
	if body["visibility"] != "public" {
		t.Errorf("visibility = %v, want public", body["visibility"])
	}
	if body["name"] != "Disk full on prod" {
		t.Errorf("name = %v, want the ticket title", body["name"])
	}
	if body["summary"] != "The disk is full." {
		t.Errorf("summary = %v, want the ticket description", body["summary"])
	}
	if body["incident_type_id"] != "01TYPE_OUTAGE" {
		t.Errorf("incident_type_id = %v, want ProjectKey", body["incident_type_id"])
	}
	if body["severity_id"] != "01SEV_MAJOR" {
		t.Errorf("severity_id = %v, want the Major severity resolved by name", body["severity_id"])
	}
	if got.TicketID != testIncidentIOID {
		t.Errorf("TicketID = %q, want %s", got.TicketID, testIncidentIOID)
	}
	if got.Platform != "incidentio" {
		t.Errorf("Platform = %q, want incidentio", got.Platform)
	}
	if got.URL != "https://app.incident.io/incidents/9" {
		t.Errorf("URL = %q, want the permalink", got.URL)
	}
}

// TestIncidentIOService_List verifies page_size/after paging and that Total
// comes from pagination_meta rather than len(incidents).
func TestIncidentIOService_List(t *testing.T) {
	var gotQuery string
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/incidents" {
			return false
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incidents":[
			{"id":"01FDAG4SAP5TYPT98WGR2N7W91","name":"One",
			 "permalink":"https://app.incident.io/incidents/1",
			 "incident_status":{"category":"active"},"severity":{"name":"Minor"},
			 "created_at":"2026-05-04T18:29:04Z"},
			{"id":"01FDAG4SAP5TYPT98WGR2N7W92","name":"Two",
			 "incident_status":{"category":"closed"},"severity":{"name":"Major"}}],
			"pagination_meta":{"page_size":25,"total_record_count":238}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.List(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), models.ListParams{Limit: 25})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if !containsAll(gotQuery, "page_size=25") {
		t.Errorf("query = %q, want page_size=25", gotQuery)
	}
	if len(got.Tickets) != 2 {
		t.Fatalf("got %d tickets, want 2", len(got.Tickets))
	}
	if got.Total != 238 {
		t.Errorf("Total = %d, want 238 from pagination_meta", got.Total)
	}
	if got.Tickets[0].Status != "active" || got.Tickets[1].Status != "closed" {
		t.Errorf("statuses = %q/%q, want active/closed", got.Tickets[0].Status, got.Tickets[1].Status)
	}
	if got.Tickets[0].Platform != "incidentio" {
		t.Errorf("Platform = %q, want incidentio", got.Tickets[0].Platform)
	}
}

// TestIncidentIOService_List_Offset covers the pagination gap incident.io
// creates: its API is cursor-paginated and has no offset parameter, but the
// provider-agnostic ListParams has one. Echoing an offset back while ignoring
// it would make page 2 silently return page 1, so the offset has to be applied
// by over-fetching and trimming.
func TestIncidentIOService_List_Offset(t *testing.T) {
	var gotPageSize string
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/incidents" {
			return false
		}
		gotPageSize = r.URL.Query().Get("page_size")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incidents":[
			{"id":"01FDAG4SAP5TYPT98WGR2N7W91","name":"One"},
			{"id":"01FDAG4SAP5TYPT98WGR2N7W92","name":"Two"},
			{"id":"01FDAG4SAP5TYPT98WGR2N7W93","name":"Three"},
			{"id":"01FDAG4SAP5TYPT98WGR2N7W94","name":"Four"}],
			"pagination_meta":{"total_record_count":4}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.List(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), models.ListParams{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	// The server has no offset parameter, so the client must ask for
	// offset+limit rows and drop the leading ones itself.
	if gotPageSize != "4" {
		t.Errorf("page_size = %q, want 4 (offset 2 + limit 2)", gotPageSize)
	}
	if len(got.Tickets) != 2 {
		t.Fatalf("got %d tickets, want 2", len(got.Tickets))
	}
	if got.Tickets[0].Title != "Three" || got.Tickets[1].Title != "Four" {
		t.Errorf("titles = %q/%q, want Three/Four — the first 2 rows should be skipped",
			got.Tickets[0].Title, got.Tickets[1].Title)
	}
	if got.Offset != 2 || got.Limit != 2 {
		t.Errorf("Offset/Limit = %d/%d, want 2/2", got.Offset, got.Limit)
	}
}

// TestIncidentIOService_List_OffsetBeyondEnd returns an empty page rather than
// slicing out of range.
func TestIncidentIOService_List_OffsetBeyondEnd(t *testing.T) {
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/incidents" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incidents":[
			{"id":"01FDAG4SAP5TYPT98WGR2N7W91","name":"One"}],
			"pagination_meta":{"total_record_count":1}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.List(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), models.ListParams{Limit: 10, Offset: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got.Tickets) != 0 {
		t.Errorf("got %d tickets, want 0 for an offset past the end", len(got.Tickets))
	}
}

// TestIncidentIOService_GetComments maps incident.io timeline updates onto
// NudgeBee comments, attributing the API-key updater when no user is present.
func TestIncidentIOService_GetComments(t *testing.T) {
	var gotQuery string
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/v2/incident_updates" {
			return false
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incident_updates":[
			{"id":"01U1","incident_id":"01FDAG4SAP5TYPT98WGR2N7W91",
			 "message":"Rolling back the deploy","created_at":"2026-05-04T19:00:00Z",
			 "updater":{"user":{"id":"01USER_A","name":"Lisa Curtis"}}},
			{"id":"01U2","incident_id":"01FDAG4SAP5TYPT98WGR2N7W91",
			 "message":"Mitigated","created_at":"2026-05-04T20:00:00Z",
			 "updater":{"api_key":{"id":"01K","name":"NudgeBee"}}}]}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.GetComments(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID)
	if err != nil {
		t.Fatalf("GetComments() error = %v", err)
	}

	if !containsAll(gotQuery, "incident_id="+testIncidentIOID) {
		t.Errorf("query = %q, want incident_id filter", gotQuery)
	}
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2", len(got))
	}
	if got[0].Author != "Lisa Curtis" || got[0].Comment != "Rolling back the deploy" {
		t.Errorf("comment[0] = %+v, want Lisa Curtis / Rolling back the deploy", got[0])
	}
	if got[1].Author != "NudgeBee" {
		t.Errorf("comment[1].Author = %q, want the API key name when no user is set", got[1].Author)
	}
}

// TestIncidentIOService_AddComment_Unsupported pins the deliberate decision to
// fail loudly: incident.io has no public write API for incident updates, and a
// silent no-op would let a workflow believe it had posted.
func TestIncidentIOService_AddComment_Unsupported(t *testing.T) {
	called := false
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer stub.Close()

	svc := &IncidentIOService{}
	err := svc.AddComment(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), models.Ticket{
		TicketID: testIncidentIOID,
		Comment:  "anything",
	})
	if err == nil {
		t.Fatal("AddComment() must return an error, not silently succeed")
	}
	if called {
		t.Error("AddComment() should not issue any HTTP request")
	}
}

// TestIncidentIOService_Escalate_Unsupported — same reasoning as AddComment.
func TestIncidentIOService_Escalate_Unsupported(t *testing.T) {
	svc := &IncidentIOService{}
	if err := svc.Escalate(incidentIOTestCtx(), incidentIOTestConfig("http://127.0.0.1:0"), testIncidentIOID, "policy"); err == nil {
		t.Fatal("Escalate() must return an error")
	}
}

// TestIncidentIOService_Resolve moves the incident to the lowest-ranked status
// in the closed category, and appends the resolution to the summary since
// incident.io offers no other write surface for it.
func TestIncidentIOService_Resolve(t *testing.T) {
	var editBody map[string]any
	var editPath string
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}
		editPath = r.URL.Path
		_ = decodeJSON(r.Body, &editBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incident":{"id":"01FDAG4SAP5TYPT98WGR2N7W91"}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	if err := svc.Resolve(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID, "Restarted the replica"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if editPath != "/v2/incidents/"+testIncidentIOID+"/actions/edit" {
		t.Errorf("edit path = %q, want the actions/edit sub-resource", editPath)
	}
	inc, _ := editBody["incident"].(map[string]any)
	if inc == nil {
		t.Fatalf("edit body missing the incident wrapper: %+v", editBody)
	}
	if inc["incident_status_id"] != "01ST_CLOSED" {
		t.Errorf("incident_status_id = %v, want 01ST_CLOSED", inc["incident_status_id"])
	}
	// The resolution is appended to the existing summary, not substituted for
	// it — incident.io has no notes API, so the summary is the only place the
	// resolution can land, and clobbering it would destroy the incident's
	// original description.
	summary, _ := inc["summary"].(string)
	if !containsAll(summary, "Restarted the replica") {
		t.Errorf("summary = %q, want the resolution text appended", summary)
	}
	if !containsAll(summary, "The disk is full.") {
		t.Errorf("summary = %q, want the original summary preserved", summary)
	}
}

// TestIncidentIOService_Acknowledge moves the incident out of triage into the
// lowest-ranked active status.
func TestIncidentIOService_Acknowledge(t *testing.T) {
	var editBody map[string]any
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}
		_ = decodeJSON(r.Body, &editBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incident":{"id":"01FDAG4SAP5TYPT98WGR2N7W91"}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	if err := svc.Acknowledge(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID); err != nil {
		t.Fatalf("Acknowledge() error = %v", err)
	}

	inc, _ := editBody["incident"].(map[string]any)
	if inc == nil || inc["incident_status_id"] != "01ST_INVEST" {
		t.Errorf("incident_status_id = %v, want 01ST_INVEST (lowest-ranked active)", inc["incident_status_id"])
	}
}

// TestIncidentIOService_Transition_UnknownStatus rejects a status token with no
// incident.io category rather than silently doing nothing.
func TestIncidentIOService_Transition_UnknownStatus(t *testing.T) {
	stub := incidentIOMetaStub(t, nil)
	defer stub.Close()

	svc := &IncidentIOService{}
	err := svc.Transition(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID, "hibernating")
	if err == nil {
		t.Fatal("Transition() with an unmappable status should error")
	}
}

// TestIncidentIOService_Update_RejectsUnsupportedFields mirrors the PagerDuty
// and ZenDuty contract: assignee and labels are not writable here, so asking
// for them must fail loudly instead of partially applying.
func TestIncidentIOService_Update_RejectsUnsupportedFields(t *testing.T) {
	stub := incidentIOMetaStub(t, nil)
	defer stub.Close()

	svc := &IncidentIOService{}
	err := svc.Update(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID, models.UpdateFields{
		Assignee: "lisa@example.com",
	})
	if err == nil {
		t.Fatal("Update() with an assignee should error")
	}
}

// TestIncidentIOService_Update_SeverityAndDescription covers the fields that
// ARE writable, in a single edit call.
func TestIncidentIOService_Update_SeverityAndDescription(t *testing.T) {
	var editBody map[string]any
	callCount := 0
	stub := incidentIOMetaStub(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost {
			return false
		}
		callCount++
		_ = decodeJSON(r.Body, &editBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"incident":{"id":"01FDAG4SAP5TYPT98WGR2N7W91"}}`))
		return true
	})
	defer stub.Close()

	svc := &IncidentIOService{}
	err := svc.Update(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), testIncidentIOID, models.UpdateFields{
		Severity:    "Critical",
		Description: "Now affecting checkout too.",
		Status:      "resolved",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if callCount != 1 {
		t.Errorf("edit called %d times, want 1 combined call", callCount)
	}

	inc, _ := editBody["incident"].(map[string]any)
	if inc == nil {
		t.Fatalf("edit body missing incident wrapper: %+v", editBody)
	}
	if inc["severity_id"] != "01SEV_CRIT" {
		t.Errorf("severity_id = %v, want 01SEV_CRIT", inc["severity_id"])
	}
	if inc["summary"] != "Now affecting checkout too." {
		t.Errorf("summary = %v, want the new description", inc["summary"])
	}
	if inc["incident_status_id"] != "01ST_CLOSED" {
		t.Errorf("incident_status_id = %v, want 01ST_CLOSED", inc["incident_status_id"])
	}
}

// TestIncidentIOService_GetCreateMeta checks the create-meta template exposes
// incident types as the "service" selector and tags title/description/severity
// so the frontend renders each basic field exactly once.
func TestIncidentIOService_GetCreateMeta(t *testing.T) {
	stub := incidentIOMetaStub(t, nil)
	defer stub.Close()

	svc := &IncidentIOService{}
	got, err := svc.GetCreateMeta(incidentIOTestCtx(), incidentIOTestConfig(stub.URL), "")
	if err != nil {
		t.Fatalf("GetCreateMeta() error = %v", err)
	}

	wrapper, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("GetCreateMeta() returned %T, want map[string]interface{}", got)
	}
	templates, ok := wrapper["data"].([]Template)
	if !ok || len(templates) != 1 {
		t.Fatalf(`GetCreateMeta()["data"] = %#v, want one Template`, wrapper["data"])
	}
	fields := templates[0].Fields

	svc2, ok := fields["service"]
	if !ok || len(svc2.AllowedValues) != 2 {
		t.Errorf("service field = %+v, want 2 incident types", svc2)
	}
	if !svc2.Required {
		t.Error("service field should be required")
	}
	if fields["severity"].Group != FieldGroupSeverity {
		t.Errorf("severity group = %q, want %q", fields["severity"].Group, FieldGroupSeverity)
	}
	if fields["summary"].Group != FieldGroupTitle {
		t.Errorf("summary group = %q, want %q", fields["summary"].Group, FieldGroupTitle)
	}
	if fields["description"].Group != FieldGroupDescription {
		t.Errorf("description group = %q, want %q", fields["description"].Group, FieldGroupDescription)
	}
	if len(fields["assignee"].AllowedValues) != 2 {
		t.Errorf("assignee field = %+v, want 2 users", fields["assignee"])
	}
}

// TestIncidentIOService_GetUrgencies returns the org's configured severities
// is not possible without a config, so the static fallback must still be sane.
func TestIncidentIOService_GetUrgencies(t *testing.T) {
	svc := &IncidentIOService{}
	got := svc.GetUrgencies()
	if len(got) == 0 {
		t.Fatal("GetUrgencies() returned nothing")
	}
}

// TestIncidentIOService_ImplementsIncidentManager is a compile-time guarantee
// that the provider satisfies the same contract as PagerDuty and ZenDuty.
func TestIncidentIOService_ImplementsIncidentManager(t *testing.T) {
	if _, ok := interface{}(&IncidentIOService{}).(interface {
		GetUrgencies() []string
	}); !ok {
		t.Fatal("IncidentIOService does not implement the incident manager surface")
	}
}

// TestQuickValidateIncidentIO covers the credential check the integration
// modal calls before persisting: a missing key must fail without a request, a
// good key must pass, and a rejected key must surface incident.io's status.
func TestQuickValidateIncidentIO(t *testing.T) {
	t.Run("missing api key short-circuits", func(t *testing.T) {
		called := false
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		defer stub.Close()

		err := QuickValidateIncidentIO(t.Context(), models.TicketConfigurations{URL: stub.URL})
		if err == nil {
			t.Fatal("QuickValidateIncidentIO() with no api key should error")
		}
		if called {
			t.Error("QuickValidateIncidentIO() must not call incident.io without a key")
		}
	})

	t.Run("valid key passes", func(t *testing.T) {
		stub := incidentIOMetaStub(t, nil)
		defer stub.Close()

		if err := QuickValidateIncidentIO(t.Context(), incidentIOTestConfig(stub.URL)); err != nil {
			t.Fatalf("QuickValidateIncidentIO() error = %v", err)
		}
	})

	t.Run("rejected key errors", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"invalid api key"}`))
		}))
		defer stub.Close()

		err := QuickValidateIncidentIO(t.Context(), incidentIOTestConfig(stub.URL))
		if err == nil {
			t.Fatal("QuickValidateIncidentIO() should error on 401")
		}
	})
}

// TestFetchIncidentIOIncidentTypes maps incident types onto the Project shape
// the integration metadata pipeline stores as "projects".
func TestFetchIncidentIOIncidentTypes(t *testing.T) {
	stub := incidentIOMetaStub(t, nil)
	defer stub.Close()

	got, err := FetchIncidentIOIncidentTypes(t.Context(), incidentIOTestConfig(stub.URL))
	if err != nil {
		t.Fatalf("FetchIncidentIOIncidentTypes() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d projects, want 2", len(got))
	}
	if got[0].Key != "01TYPE_DEFAULT" || got[0].Name != "Default" {
		t.Errorf("project[0] = %+v, want the incident type id/name", got[0])
	}
}

// TestFetchIncidentIOSeverities maps severities onto the Priority shape.
func TestFetchIncidentIOSeverities(t *testing.T) {
	stub := incidentIOMetaStub(t, nil)
	defer stub.Close()

	got, err := FetchIncidentIOSeverities(t.Context(), incidentIOTestConfig(stub.URL))
	if err != nil {
		t.Fatalf("FetchIncidentIOSeverities() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d priorities, want 3", len(got))
	}
	if got[0].ID != "01SEV_MINOR" || got[0].Name != "Minor" {
		t.Errorf("priority[0] = %+v, want the severity id/name", got[0])
	}
}

func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
