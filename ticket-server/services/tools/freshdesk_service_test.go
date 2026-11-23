package tools

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"

	"github.com/gin-gonic/gin"
)

func freshdeskTestCtx() *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func freshdeskTestConfig(serverURL string) models.TicketConfigurations {
	return models.TicketConfigurations{
		URL:      serverURL,
		Password: "api-key-123",
		AuthType: "basic",
	}
}

// TestFreshdeskService_Get verifies the basic-auth quirk (api_key:X), the
// include=conversations query, int→string status/priority mapping, field
// mapping (description_text, group_id, requester_id), that a responder_id
// resolves to the agent's display name via the separate /agents lookup, the
// agent URL, and that Raw is populated.
func TestFreshdeskService_Get(t *testing.T) {
	var gotUser, gotPass, gotInclude string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets/42", func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		gotInclude = r.URL.Query().Get("include")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 42,
			"subject": "Disk full on prod",
			"description": "<div>The disk is full.</div>",
			"description_text": "The disk is full.",
			"status": 2,
			"priority": 3,
			"tags": ["infra", "urgent"],
			"group_id": 501,
			"responder_id": 700,
			"requester_id": 900,
			"created_at": "2026-05-04T18:29:04Z",
			"updated_at": "2026-05-05T09:15:00Z"
		}`))
	})
	mux.HandleFunc("/api/v2/agents", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":700,"contact":{"name":"Alice Agent","email":"alice@x.com"}}]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "42")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if gotUser != "api-key-123" || gotPass != "X" {
		t.Errorf("basic auth = %q:%q, want api-key-123:X", gotUser, gotPass)
	}
	if gotInclude != "conversations" {
		t.Errorf("include = %q, want conversations", gotInclude)
	}
	if got.TicketID != "42" || got.ID != "42" {
		t.Errorf("id = %q/%q, want 42", got.TicketID, got.ID)
	}
	if got.Title != "Disk full on prod" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Description != "The disk is full." {
		t.Errorf("description = %q, want plain description_text", got.Description)
	}
	if got.Status != "Open" {
		t.Errorf("status = %q, want Open (2)", got.Status)
	}
	if got.Severity != "High" {
		t.Errorf("severity = %q, want High (3)", got.Severity)
	}
	if got.Tags != "infra,urgent" {
		t.Errorf("tags = %q, want infra,urgent", got.Tags)
	}
	if got.ProjectKey != "501" {
		t.Errorf("project_key = %q, want 501 (group_id)", got.ProjectKey)
	}
	if got.Assignee != "Alice Agent" {
		t.Errorf("assignee = %q, want \"Alice Agent\" (resolved from responder_id 700 via /agents)", got.Assignee)
	}
	if got.Reporter != "900" {
		t.Errorf("reporter = %q, want 900 (requester_id)", got.Reporter)
	}
	if got.Platform != "freshdesk" {
		t.Errorf("platform = %q", got.Platform)
	}
	if want := stub.URL + "/a/tickets/42"; got.URL != want {
		t.Errorf("url = %q, want %q", got.URL, want)
	}
	if got.Raw == nil || got.Raw["group_id"] == nil {
		t.Errorf("Raw not populated with source fields: %v", got.Raw)
	}
	if got.CreatedAt == nil || got.UpdatedAt == nil {
		t.Errorf("timestamps not parsed: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

// TestFreshdeskService_Get_Attachments verifies that Get flattens ticket-level and
// conversation-level file attachments plus Freshdesk-hosted inline images from the
// description HTML, tagging each with the right Source, and filters out foreign
// (non-Freshdesk) inline images such as email-signature logos.
func TestFreshdeskService_Get_Attachments(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": 55,
			"subject": "With attachments",
			"description": "<div>See <img src=\"https://indattachment.freshdesk.com/inline/attachment?token=abc&amp;expires=123\" class=\"fr-fic\"> and a logo <img src=\"https://cdn.example.com/signature-logo.png\"></div>",
			"description_text": "See screenshot",
			"status": 2,
			"priority": 1,
			"attachments": [
				{"name": "app-error.log", "content_type": "text/plain", "size": 20481, "attachment_url": "https://s3.amazonaws.com/cdn.freshdesk.com/file1?Signature=x"}
			],
			"conversations": [
				{"id": 9001, "body_text": "note", "attachments": [
					{"name": "trace.json", "content_type": "application/json", "size": 512, "attachment_url": "https://s3.amazonaws.com/cdn.freshdesk.com/file2?Signature=y"}
				]}
			]
		}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "55")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(got.Attachments) != 3 {
		t.Fatalf("got %d attachments, want 3 (ticket file + conversation file + inline image): %+v", len(got.Attachments), got.Attachments)
	}

	bySource := map[string]models.Attachment{}
	for _, a := range got.Attachments {
		bySource[a.Source] = a
	}

	if a := bySource["ticket"]; a.Name != "app-error.log" || a.URL != "https://s3.amazonaws.com/cdn.freshdesk.com/file1?Signature=x" || a.ContentType != "text/plain" || a.Size != 20481 {
		t.Errorf("ticket attachment = %+v", a)
	}
	if a := bySource["conversation:9001"]; a.Name != "trace.json" || a.URL != "https://s3.amazonaws.com/cdn.freshdesk.com/file2?Signature=y" {
		t.Errorf("conversation attachment = %+v", a)
	}
	if a := bySource["inline"]; a.URL != "https://indattachment.freshdesk.com/inline/attachment?token=abc&expires=123" || a.ContentType != "image" {
		t.Errorf("inline image attachment (HTML entity in src should be unescaped) = %+v", a)
	}
	for _, a := range got.Attachments {
		if strings.Contains(a.URL, "signature-logo") {
			t.Errorf("foreign (non-Freshdesk) inline image should be filtered out: %+v", a)
		}
	}
}

// TestFreshdeskService_Get_Unauthorized verifies a 401 surfaces a clear auth error.
func TestFreshdeskService_Get_Unauthorized(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid credentials"}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	_, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "42")
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error = %v, want authentication failed", err)
	}
}

// A deleted Freshdesk ticket (404) must surface as ticket.ErrNotFound so the
// ticket-status sync job can skip it without counting toward its
// consecutive-failure abort guard.
func TestFreshdeskService_Get_NotFoundIsErrNotFound(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Ticket not found"}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	_, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "42")
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap ticket.ErrNotFound", err)
	}
}

// TestFreshdeskService_Get_InvalidID rejects non-numeric ids before any request.
func TestFreshdeskService_Get_InvalidID(t *testing.T) {
	svc := &FreshdeskService{}
	_, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig("http://127.0.0.1:1"), "42; DROP")
	if err == nil {
		t.Fatal("expected error for non-numeric ticket id")
	}
}

// TestFreshdeskService_List_ViaTickets verifies the no-filter path hits /tickets
// with per_page/order params and maps rows.
func TestFreshdeskService_List_ViaTickets(t *testing.T) {
	var gotPath, gotQuery string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"id":1,"subject":"A","status":2,"priority":1,"created_at":"2026-01-01T00:00:00Z"},
			{"id":2,"subject":"B","status":5,"priority":4,"created_at":"2026-01-02T00:00:00Z"}
		]`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	res, err := svc.List(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.ListParams{
		Limit: 50, SortBy: "updated_at", SortOrder: "asc",
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if gotPath != "/api/v2/tickets" {
		t.Errorf("path = %q, want /api/v2/tickets (no filters)", gotPath)
	}
	for _, want := range []string{"per_page=50", "order_by=updated_at", "order_type=asc"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
	if len(res.Tickets) != 2 {
		t.Fatalf("got %d tickets, want 2", len(res.Tickets))
	}
	if res.Tickets[0].Status != "Open" || res.Tickets[1].Status != "Closed" {
		t.Errorf("statuses = %q,%q want Open,Closed", res.Tickets[0].Status, res.Tickets[1].Status)
	}
	if res.Tickets[0].Severity != "Low" || res.Tickets[1].Severity != "Urgent" {
		t.Errorf("severities = %q,%q want Low,Urgent", res.Tickets[0].Severity, res.Tickets[1].Severity)
	}
	if res.Limit != 50 {
		t.Errorf("limit = %d, want 50", res.Limit)
	}
}

// TestFreshdeskService_List_ResolvesAssigneeNames verifies List resolves each
// row's responder_id to the agent's display name with a single /agents call
// (not one per ticket), and falls back to the raw id for a responder_id no
// longer in the agents list (e.g. a deactivated agent).
func TestFreshdeskService_List_ResolvesAssigneeNames(t *testing.T) {
	var agentsCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"subject":"A","status":2,"priority":1,"responder_id":700,"created_at":"2026-01-01T00:00:00Z"},
			{"id":2,"subject":"B","status":2,"priority":1,"responder_id":999,"created_at":"2026-01-02T00:00:00Z"},
			{"id":3,"subject":"C","status":2,"priority":1,"created_at":"2026-01-03T00:00:00Z"}
		]`))
	})
	mux.HandleFunc("/api/v2/agents", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&agentsCalls, 1)
		_, _ = w.Write([]byte(`[{"id":700,"contact":{"name":"Alice Agent","email":"alice@x.com"}}]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	res, err := svc.List(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.ListParams{Limit: 50})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if atomic.LoadInt32(&agentsCalls) != 1 {
		t.Errorf("agents endpoint called %d times, want exactly 1 for the whole page", agentsCalls)
	}
	if len(res.Tickets) != 3 {
		t.Fatalf("got %d tickets, want 3", len(res.Tickets))
	}
	if res.Tickets[0].Assignee != "Alice Agent" {
		t.Errorf("ticket[0].Assignee = %q, want \"Alice Agent\" (resolved)", res.Tickets[0].Assignee)
	}
	if res.Tickets[1].Assignee != "999" {
		t.Errorf("ticket[1].Assignee = %q, want \"999\" (unresolvable id falls back to raw id)", res.Tickets[1].Assignee)
	}
	if res.Tickets[2].Assignee != "" {
		t.Errorf("ticket[2].Assignee = %q, want empty (no responder_id)", res.Tickets[2].Assignee)
	}
}

// TestFreshdeskService_List_ViaSearch verifies filters route to /search/tickets
// with a status:/priority: query and read the {results,total} envelope.
func TestFreshdeskService_List_ViaSearch(t *testing.T) {
	var gotPath, gotQuery string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"total":1,"results":[{"id":7,"subject":"S","status":2,"priority":3}]}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	res, err := svc.List(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.ListParams{
		Status: "open", Priority: "high", Limit: 20,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if gotPath != "/api/v2/search/tickets" {
		t.Errorf("path = %q, want /api/v2/search/tickets", gotPath)
	}
	if !strings.Contains(gotQuery, "status:2") || !strings.Contains(gotQuery, "priority:3") {
		t.Errorf("search query = %q, want status:2 AND priority:3", gotQuery)
	}
	if res.Total != 1 || len(res.Tickets) != 1 || res.Tickets[0].TicketID != "7" {
		t.Errorf("unexpected search result: total=%d tickets=%+v", res.Total, res.Tickets)
	}
}

// TestFreshdeskService_Create verifies the create payload (int priority/status,
// email requester, group_id/responder_id/tags) and the mapped response.
func TestFreshdeskService_Create(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"subject":"New","status":2,"priority":2,"created_at":"2026-06-01T00:00:00Z"}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.Create(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.Ticket{
		Title: "New", Description: "desc", Severity: "High",
		Reporter: "user@example.com", ProjectKey: "501", Assignee: "700", Tags: "a,b",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v2/tickets" {
		t.Errorf("request = %s %s, want POST /api/v2/tickets", gotMethod, gotPath)
	}
	if gotBody["subject"] != "New" {
		t.Errorf("subject = %v", gotBody["subject"])
	}
	if gotBody["priority"] != float64(3) {
		t.Errorf("priority = %v, want 3 (High)", gotBody["priority"])
	}
	if gotBody["status"] != float64(2) {
		t.Errorf("status = %v, want 2 (default Open)", gotBody["status"])
	}
	if gotBody["email"] != "user@example.com" {
		t.Errorf("email = %v, want requester email", gotBody["email"])
	}
	if gotBody["group_id"] != float64(501) {
		t.Errorf("group_id = %v, want 501", gotBody["group_id"])
	}
	if gotBody["responder_id"] != float64(700) {
		t.Errorf("responder_id = %v, want 700", gotBody["responder_id"])
	}
	if tags, ok := gotBody["tags"].([]any); !ok || len(tags) != 2 {
		t.Errorf("tags = %v, want [a b]", gotBody["tags"])
	}
	if got.TicketID != "123" {
		t.Errorf("ticket id = %q, want 123", got.TicketID)
	}
	if got.Status != "Open" || got.Severity != "Medium" {
		t.Errorf("mapped status/severity = %q/%q, want Open/Medium", got.Status, got.Severity)
	}
	if want := stub.URL + "/a/tickets/123"; got.URL != want {
		t.Errorf("url = %q, want %q", got.URL, want)
	}
}

// TestFreshdeskService_Create_ResolvesAssigneeName verifies the created ticket's
// Assignee is resolved to the agent's display name (not left as the raw
// responder_id) - this is what the caller persists to the tickets table, so an
// unresolved id here would show up as a numeric id in the UI forever, mirroring
// Jira's CreateJiraIssue which already returns a DisplayName.
func TestFreshdeskService_Create_ResolvesAssigneeName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/tickets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":123,"subject":"New","status":2,"priority":2,"responder_id":700,"created_at":"2026-06-01T00:00:00Z"}`))
	})
	mux.HandleFunc("/api/v2/agents", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":700,"contact":{"name":"Alice Agent","email":"alice@x.com"}}]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.Create(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.Ticket{
		Title: "New", Reporter: "user@example.com", Assignee: "700",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.Assignee != "Alice Agent" {
		t.Errorf("Assignee = %q, want \"Alice Agent\" (resolved from responder_id 700, not left raw)", got.Assignee)
	}
}

// TestFreshdeskService_Create_CustomFieldsAndType verifies per-form custom
// fields land under custom_fields, the native ticket type goes top-level, and
// base-field keys (group/assignee/priority/...) are skipped rather than
// leaking into custom_fields.
func TestFreshdeskService_Create_CustomFieldsAndType(t *testing.T) {
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":124,"subject":"New","status":2,"priority":2,"created_at":"2026-06-01T00:00:00Z"}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	_, err := svc.Create(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.Ticket{
		Title:    "New",
		Reporter: "user@example.com",
		AdditionalFields: map[string]interface{}{
			"type":      "Incident",
			"cf_region": "us-east-1",
			"group":     "501",            // base field - must NOT leak into custom_fields
			"requester": "shadowed@x.com", // Reporter wins (set above); must NOT leak into custom_fields either way
			"assignee":  "700",            // normally extracted into Ticket.Assignee before Create(); must NOT leak if sent inline
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gotBody["type"] != "Incident" {
		t.Errorf("type = %v, want top-level \"Incident\"", gotBody["type"])
	}
	customFields, ok := gotBody["custom_fields"].(map[string]any)
	if !ok {
		t.Fatalf("custom_fields missing or wrong shape: %#v", gotBody["custom_fields"])
	}
	if customFields["cf_region"] != "us-east-1" {
		t.Errorf("custom_fields[cf_region] = %v, want us-east-1", customFields["cf_region"])
	}
	if _, leaked := customFields["group"]; leaked {
		t.Errorf("custom_fields should not carry the base \"group\" key: %v", customFields)
	}
	if _, leaked := customFields["requester"]; leaked {
		t.Errorf("custom_fields should not carry the \"requester\" key: %v", customFields)
	}
	if _, leaked := customFields["assignee"]; leaked {
		t.Errorf("custom_fields should not carry the \"assignee\" key: %v", customFields)
	}
	if _, leaked := customFields["type"]; leaked {
		t.Errorf("custom_fields should not carry \"type\" (sent top-level instead): %v", customFields)
	}
}

// TestFreshdeskService_Create_RequesterFromAdditionalFields verifies that with
// no top-level Reporter, the dynamic "requester" field (every Freshdesk form's
// default_requester entry, rendered generically now - see freshdeskBaseFields)
// resolves the ticket's requester, matching the Reporter-based email/id rules.
func TestFreshdeskService_Create_RequesterFromAdditionalFields(t *testing.T) {
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":125,"subject":"New","status":2,"priority":2,"created_at":"2026-06-01T00:00:00Z"}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	_, err := svc.Create(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.Ticket{
		Title:            "New",
		AdditionalFields: map[string]interface{}{"requester": "fromform@example.com"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if gotBody["email"] != "fromform@example.com" {
		t.Errorf("email = %v, want fromform@example.com (from additional_fields.requester)", gotBody["email"])
	}
}

// TestFreshdeskService_Create_RequiresRequester verifies a create without a
// requester fails fast with a clear error and never hits the server.
func TestFreshdeskService_Create_RequiresRequester(t *testing.T) {
	svc := &FreshdeskService{}
	_, err := svc.Create(freshdeskTestCtx(), freshdeskTestConfig("http://127.0.0.1:1"), models.Ticket{Title: "X"})
	if err == nil {
		t.Fatal("expected requester-required error")
	}
	if !strings.Contains(err.Error(), "requester") {
		t.Errorf("error = %v, want requester-required message", err)
	}
}

// TestFreshdeskService_Update verifies field mapping on PUT /tickets/{id}.
func TestFreshdeskService_Update(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	err := svc.Update(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "55", models.UpdateFields{
		Status: "resolved", Severity: "Urgent", Assignee: "701",
		Description: "d", Labels: []string{"x"}, ProjectKey: "502",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v2/tickets/55" {
		t.Errorf("request = %s %s, want PUT /api/v2/tickets/55", gotMethod, gotPath)
	}
	if gotBody["status"] != float64(4) {
		t.Errorf("status = %v, want 4 (Resolved)", gotBody["status"])
	}
	if gotBody["priority"] != float64(4) {
		t.Errorf("priority = %v, want 4 (Urgent)", gotBody["priority"])
	}
	if gotBody["responder_id"] != float64(701) {
		t.Errorf("responder_id = %v, want 701", gotBody["responder_id"])
	}
	if gotBody["group_id"] != float64(502) {
		t.Errorf("group_id = %v, want 502", gotBody["group_id"])
	}
	if gotBody["description"] != "d" {
		t.Errorf("description = %v", gotBody["description"])
	}
}

// TestFreshdeskService_Transition verifies status-only PUT with an int status.
func TestFreshdeskService_Transition(t *testing.T) {
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	if err := svc.Transition(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "55", "closed"); err != nil {
		t.Fatalf("Transition() error = %v", err)
	}
	if gotBody["status"] != float64(5) {
		t.Errorf("status = %v, want 5 (Closed)", gotBody["status"])
	}
	if len(gotBody) != 1 {
		t.Errorf("transition body = %v, want only status", gotBody)
	}
}

// TestFreshdeskService_AddComment verifies a private note is posted to /notes.
func TestFreshdeskService_AddComment(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	err := svc.AddComment(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), models.Ticket{
		TicketID: "88", Comment: "line1\n<ns>/pod & up",
	})
	if err != nil {
		t.Fatalf("AddComment() error = %v", err)
	}
	if gotPath != "/api/v2/tickets/88/notes" {
		t.Errorf("path = %q, want /api/v2/tickets/88/notes", gotPath)
	}
	if gotBody["private"] != true {
		t.Errorf("private = %v, want true", gotBody["private"])
	}
	// Newlines become <br>; HTML-special chars are escaped so RCA text renders literally.
	if body, _ := gotBody["body"].(string); !strings.Contains(body, "line1<br>&lt;ns&gt;/pod &amp; up") {
		t.Errorf("body = %q, want escaped HTML with <br> line breaks", body)
	}
}

// TestFreshdeskService_GetComments verifies conversation mapping, including that
// private notes are included and labeled.
func TestFreshdeskService_GetComments(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/conversations") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[
			{"body_text":"public reply","from_email":"agent@x.com","private":false,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},
			{"body_text":"internal note","user_id":700,"private":true,"created_at":"2026-01-02T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}
		]`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.GetComments(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "88")
	if err != nil {
		t.Fatalf("GetComments() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d comments, want 2", len(got))
	}
	if got[0].Author != "agent@x.com" || got[0].Comment != "public reply" {
		t.Errorf("public comment = %+v", got[0])
	}
	if got[1].Author != "700" || !strings.HasPrefix(got[1].Comment, "[private note] ") {
		t.Errorf("private note not labeled: %+v", got[1])
	}
}

// freshdeskCreateMetaBaseMux stubs the groups/agents calls every GetCreateMeta
// path hits, regardless of whether Ticket Forms is available.
func freshdeskCreateMetaBaseMux(mux *http.ServeMux) {
	mux.HandleFunc("/api/v2/groups", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":501,"name":"Support"},{"id":502,"name":"Billing"}]`))
	})
	mux.HandleFunc("/api/v2/agents", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":700,"contact":{"name":"Alice","email":"alice@x.com"}}]`))
	})
}

// TestFreshdeskService_GetCreateMeta_NoForms verifies that when the account
// has no Ticket Forms configured (empty list, not an error), GetCreateMeta
// falls back to a single base template rather than an empty issue-type list.
func TestFreshdeskService_GetCreateMeta_NoForms(t *testing.T) {
	mux := http.NewServeMux()
	freshdeskCreateMetaBaseMux(mux)
	mux.HandleFunc("/api/v2/ticket-forms", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	meta, err := svc.GetCreateMeta(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "")
	if err != nil {
		t.Fatalf("GetCreateMeta() error = %v", err)
	}
	data, ok := meta.(map[string]interface{})["data"].([]Template)
	if !ok || len(data) != 1 || data[0].Name != "Freshdesk Ticket" {
		t.Fatalf("unexpected meta shape: %#v", meta)
	}
	fields := data[0].Fields
	if _, present := fields["group"]; present {
		t.Errorf("base fields should not carry \"group\" - it's the top-level project selector (Ticket.ProjectKey), not a create-meta field: %+v", fields["group"])
	}
	if len(fields["assignee"].AllowedValues) != 1 {
		t.Errorf("assignee allowedValues = %v, want 1 agent", fields["assignee"].AllowedValues)
	}
	if fields["priority"].Group != FieldGroupSeverity {
		t.Errorf("priority group = %q, want %q", fields["priority"].Group, FieldGroupSeverity)
	}
	if !fields["summary"].Required || !fields["requester"].Required {
		t.Errorf("summary/requester should be required")
	}
}

// TestFreshdeskService_GetCreateMeta_FallbackOnFormsError verifies a
// ticket-forms fetch failure (e.g. the feature isn't on this Freshdesk plan)
// degrades to the single base template instead of erroring the create flow.
func TestFreshdeskService_GetCreateMeta_FallbackOnFormsError(t *testing.T) {
	mux := http.NewServeMux()
	freshdeskCreateMetaBaseMux(mux)
	mux.HandleFunc("/api/v2/ticket-forms", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	meta, err := svc.GetCreateMeta(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "")
	if err != nil {
		t.Fatalf("GetCreateMeta() error = %v, want graceful fallback", err)
	}
	data, ok := meta.(map[string]interface{})["data"].([]Template)
	if !ok || len(data) != 1 || data[0].Name != "Freshdesk Ticket" {
		t.Fatalf("unexpected meta shape: %#v", meta)
	}
}

// TestFreshdeskService_TicketFormsAndFieldsRespectPerPageLimit guards against
// regressing to freshdeskListPageSize (100): /ticket-forms and /ticket_fields
// cap per_page at 50 (confirmed live - Freshdesk rejects 100 with a 400
// validation error), unlike every other list endpoint in this file. The stub
// mimics that real validation so a reintroduced per_page=100 fails the test
// the same way it silently fails against the real API (GetCreateMeta would
// degrade to the single fallback template instead of erroring).
func TestFreshdeskService_TicketFormsAndFieldsRespectPerPageLimit(t *testing.T) {
	mux := http.NewServeMux()
	freshdeskCreateMetaBaseMux(mux)
	validatePerPage := func(w http.ResponseWriter, r *http.Request) bool {
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage <= 0 || perPage > 50 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"field":"per_page","message":"It should be a Positive Integer less than or equal to 50","code":"invalid_value"}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v2/ticket-forms", func(w http.ResponseWriter, r *http.Request) {
		if !validatePerPage(w, r) {
			return
		}
		_, _ = w.Write([]byte(`[{"id":10,"title":"Report an issue"}]`))
	})
	mux.HandleFunc("/api/v2/ticket-forms/10", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":10,"title":"Report an issue","fields":[]}`))
	})
	mux.HandleFunc("/api/v2/ticket_fields", func(w http.ResponseWriter, r *http.Request) {
		if !validatePerPage(w, r) {
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	meta, err := svc.GetCreateMeta(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "")
	if err != nil {
		t.Fatalf("GetCreateMeta() error = %v", err)
	}
	data, ok := meta.(map[string]interface{})["data"].([]Template)
	if !ok || len(data) != 1 || data[0].Name != "Report an issue" {
		t.Fatalf("per_page rejected by the stub (would degrade to fallback against the real API) - meta: %#v", meta)
	}
}

// TestFreshdeskService_GetCreateMeta_PerFormFields verifies multiple Ticket
// Forms surface as one Template each, each carrying the base fields plus its
// own custom fields joined from the account-wide field definitions, with the
// native ticket type routed to the reserved "type" key and an unsupported
// field type (nested_field) dropped.
func TestFreshdeskService_GetCreateMeta_PerFormFields(t *testing.T) {
	mux := http.NewServeMux()
	freshdeskCreateMetaBaseMux(mux)
	mux.HandleFunc("/api/v2/ticket-forms", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":10,"title":"Report an issue"},{"id":11,"title":"Ask a question"}]`))
	})
	mux.HandleFunc("/api/v2/ticket-forms/10", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":10,"title":"Report an issue","fields":[
			{"id":1,"required_for_agents":true},
			{"id":2,"required_for_agents":false},
			{"id":3,"required_for_agents":false},
			{"id":4,"required_for_agents":true}
		]}`))
	})
	mux.HandleFunc("/api/v2/ticket-forms/11", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":11,"title":"Ask a question","fields":[
			{"id":2,"required_for_agents":false},
			{"id":4,"required_for_agents":false}
		]}`))
	})
	mux.HandleFunc("/api/v2/ticket_fields", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"cf_region","label":"Region","type":"custom_dropdown","choices":["us-east-1","eu-west-1"]},
			{"id":2,"name":"ticket_type","label":"Type","type":"default_ticket_type","choices":["Question","Problem"]},
			{"id":3,"name":"cf_dependent","label":"Dependent","type":"nested_field"},
			{"id":4,"name":"requester","label":"Search a requester","type":"default_requester"}
		]`))
	})
	stub := httptest.NewServer(mux)
	defer stub.Close()

	svc := &FreshdeskService{}
	meta, err := svc.GetCreateMeta(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "")
	if err != nil {
		t.Fatalf("GetCreateMeta() error = %v", err)
	}
	data, ok := meta.(map[string]interface{})["data"].([]Template)
	if !ok || len(data) != 2 {
		t.Fatalf("unexpected meta shape: %#v", meta)
	}

	byName := map[string]Template{data[0].Name: data[0], data[1].Name: data[1]}
	report, ok := byName["Report an issue"]
	if !ok {
		t.Fatalf("missing \"Report an issue\" template: %#v", data)
	}
	region, ok := report.Fields["cf_region"]
	if !ok {
		t.Fatalf("\"Report an issue\" missing cf_region field: %#v", report.Fields)
	}
	if region.Type != "select" || !region.Required || len(region.AllowedValues) != 2 {
		t.Errorf("cf_region = %+v, want required select with 2 choices", region)
	}
	typeField, ok := report.Fields["type"]
	if !ok {
		t.Fatalf("\"Report an issue\" missing \"type\" field (from default_ticket_type): %#v", report.Fields)
	}
	if typeField.Type != "select" || len(typeField.AllowedValues) != 2 {
		t.Errorf("type field = %+v, want select with 2 choices", typeField)
	}
	if _, leaked := report.Fields["cf_dependent"]; leaked {
		t.Errorf("nested_field type should have been dropped, got %+v", report.Fields["cf_dependent"])
	}
	// Base fields (assignee, not group - see freshdeskBaseFields) still present
	// alongside the form's custom fields.
	if len(report.Fields["assignee"].AllowedValues) != 1 {
		t.Errorf("base \"assignee\" field missing/wrong on form template: %+v", report.Fields["assignee"])
	}
	if _, present := report.Fields["group"]; present {
		t.Errorf("\"group\" should not be a create-meta field - it's the top-level project selector: %+v", report.Fields["group"])
	}
	// The form's own default_requester entry overwrites freshdeskBaseFields'
	// fallback "requester" with this form's real required flag, but keeps the
	// fixed "Requester Email" name (not Freshdesk's own "Search a requester"
	// label - see freshdeskFieldToInfo).
	requester, ok := report.Fields["requester"]
	if !ok {
		t.Fatalf("\"Report an issue\" missing \"requester\" field: %#v", report.Fields)
	}
	if requester.Type != "string" || !requester.Required || requester.Name != "Requester Email" {
		t.Errorf("requester = %+v, want required string named \"Requester Email\"", requester)
	}

	ask, ok := byName["Ask a question"]
	if !ok {
		t.Fatalf("missing \"Ask a question\" template: %#v", data)
	}
	if _, present := ask.Fields["cf_region"]; present {
		t.Errorf("\"Ask a question\" should not carry cf_region (not on that form): %+v", ask.Fields)
	}
	if _, present := ask.Fields["type"]; !present {
		t.Errorf("\"Ask a question\" should carry the shared \"type\" field: %+v", ask.Fields)
	}
	// This form marks requester not-required, overriding freshdeskBaseFields'
	// Required:true fallback - confirms the override isn't hardcoded to one form.
	if ask.Fields["requester"].Required {
		t.Errorf("\"Ask a question\" requester.Required = true, want false (per that form's required_for_agents)")
	}
}

// TestFreshdeskService_RateLimitRetry verifies a 429 with Retry-After is retried.
func TestFreshdeskService_RateLimitRetry(t *testing.T) {
	var calls int32
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":9,"subject":"ok","status":2,"priority":2}`))
	}))
	defer stub.Close()

	svc := &FreshdeskService{}
	got, err := svc.Get(freshdeskTestCtx(), freshdeskTestConfig(stub.URL), "9")
	if err != nil {
		t.Fatalf("Get() error after retry = %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("server calls = %d, want 2 (one 429 + one success)", n)
	}
	if got.TicketID != "9" {
		t.Errorf("ticket id = %q, want 9", got.TicketID)
	}
}

// TestFreshdeskStatusPriorityMapping covers the int↔string mappings in both directions.
func TestFreshdeskStatusPriorityMapping(t *testing.T) {
	statusToString := map[int]string{2: "Open", 3: "Pending", 4: "Resolved", 5: "Closed", 99: "99"}
	for in, want := range statusToString {
		if got := freshdeskStatusToString(in); got != want {
			t.Errorf("freshdeskStatusToString(%d) = %q, want %q", in, got, want)
		}
	}

	statusToInt := []struct {
		in   string
		want int
		ok   bool
	}{
		{"open", 2, true}, {"Resolved", 4, true}, {"5", 5, true}, {"99", 99, true}, {"bogus", 0, false},
	}
	for _, c := range statusToInt {
		got, ok := freshdeskStatusToInt(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("freshdeskStatusToInt(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}

	priorityToString := map[int]string{1: "Low", 2: "Medium", 3: "High", 4: "Urgent"}
	for in, want := range priorityToString {
		if got := freshdeskPriorityToString(in); got != want {
			t.Errorf("freshdeskPriorityToString(%d) = %q, want %q", in, got, want)
		}
	}

	priorityToInt := map[string]int{"low": 1, "Urgent": 4, "3": 3, "bogus": 2}
	for in, want := range priorityToInt {
		if got := freshdeskPriorityToInt(in); got != want {
			t.Errorf("freshdeskPriorityToInt(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestResolveFreshdeskRequester covers requester resolution from Reporter and
// the additional_fields fallback (the only channel an agent has).
func TestResolveFreshdeskRequester(t *testing.T) {
	cases := []struct {
		name    string
		ticket  models.Ticket
		wantKey string
		wantVal any
		wantErr bool
	}{
		{"reporter email", models.Ticket{Reporter: "a@b.com"}, "email", "a@b.com", false},
		{"reporter numeric", models.Ticket{Reporter: "123"}, "requester_id", int64(123), false},
		{"additional_fields email", models.Ticket{AdditionalFields: map[string]interface{}{"email": "c@d.com"}}, "email", "c@d.com", false},
		{"additional_fields requester_id float", models.Ticket{AdditionalFields: map[string]interface{}{"requester_id": float64(456)}}, "requester_id", int64(456), false},
		{"missing requester", models.Ticket{}, "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, val, err := resolveFreshdeskRequester(c.ticket)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got key=%q val=%v", key, val)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if key != c.wantKey || val != c.wantVal {
				t.Errorf("got %q=%v, want %q=%v", key, val, c.wantKey, c.wantVal)
			}
		})
	}
}

// TestEncodeFreshdeskAdditionalFields verifies the bug found via live testing:
// a "number"-type custom field (e.g. a custom_number field like
// cf_reference_number) submitted as a string ("5", from a plain text input)
// must be coerced to a JSON number before Create() builds custom_fields, or
// Freshdesk rejects it with a 400 datatype_mismatch error. Non-number types and
// unknown keys must pass through untouched.
func TestEncodeFreshdeskAdditionalFields(t *testing.T) {
	fields := map[string]FieldInfo{
		"cf_reference_number": {Key: "cf_reference_number", Type: "number"},
		"cf_region":           {Key: "cf_region", Type: "select"},
		"cf_active": {
			Key:  "cf_active",
			Type: "select",
			AllowedValues: []interface{}{
				map[string]interface{}{"id": "true", "name": "Yes", "value": true},
				map[string]interface{}{"id": "false", "name": "No", "value": false},
			},
		},
	}

	cases := []struct {
		name string
		in   map[string]interface{}
		want map[string]interface{}
	}{
		{
			name: "number field: string coerced to a JSON number",
			in:   map[string]interface{}{"cf_reference_number": "5"},
			want: map[string]interface{}{"cf_reference_number": float64(5)},
		},
		{
			name: "number field: already-numeric value passes through",
			in:   map[string]interface{}{"cf_reference_number": float64(5)},
			want: map[string]interface{}{"cf_reference_number": float64(5)},
		},
		{
			name: "number field: non-numeric string falls back unchanged (Freshdesk's own error is the signal)",
			in:   map[string]interface{}{"cf_reference_number": "not-a-number"},
			want: map[string]interface{}{"cf_reference_number": "not-a-number"},
		},
		{
			name: "select field with no AllowedValues passes through untouched",
			in:   map[string]interface{}{"cf_region": "us-east-1"},
			want: map[string]interface{}{"cf_region": "us-east-1"},
		},
		{
			name: "select field: id resolves to its AllowedValues entry's value (custom_checkbox's string id -> real boolean)",
			in:   map[string]interface{}{"cf_active": "true"},
			want: map[string]interface{}{"cf_active": true},
		},
		{
			name: "select field: the other option resolves too, not just the first",
			in:   map[string]interface{}{"cf_active": "false"},
			want: map[string]interface{}{"cf_active": false},
		},
		{
			name: "select field: an id with no matching AllowedValues entry passes through unchanged",
			in:   map[string]interface{}{"cf_active": "stale-id"},
			want: map[string]interface{}{"cf_active": "stale-id"},
		},
		{
			name: "unknown key passes through",
			in:   map[string]interface{}{"cf_unknown": "5"},
			want: map[string]interface{}{"cf_unknown": "5"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeFreshdeskAdditionalFields(fields, tc.in)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("encoded = %s, want %s", gotJSON, wantJSON)
			}
		})
	}

	t.Run("nil meta passes everything through", func(t *testing.T) {
		in := map[string]interface{}{"cf_reference_number": "5"}
		got := EncodeFreshdeskAdditionalFields(nil, in)
		if got["cf_reference_number"] != "5" {
			t.Errorf("got %v, want raw passthrough", got["cf_reference_number"])
		}
	})
}

// TestFreshdeskFieldToInfo_CustomCheckbox verifies the synthesized boolean
// select's AllowedValues carry a real JSON boolean in "value" (what
// EncodeFreshdeskAdditionalFields substitutes in) while "id" stays the string
// TicketFormComponent selects and submits by.
func TestFreshdeskFieldToInfo_CustomCheckbox(t *testing.T) {
	fi, ok := freshdeskFieldToInfo(freshdeskTicketFieldDef{
		ID: 1, Name: "cf_active", Label: "Active", Type: "custom_checkbox",
	}, true)
	if !ok {
		t.Fatal("expected custom_checkbox to be supported")
	}
	if len(fi.AllowedValues) != 2 {
		t.Fatalf("AllowedValues = %+v, want 2 entries", fi.AllowedValues)
	}
	for _, av := range fi.AllowedValues {
		entry, ok := av.(map[string]interface{})
		if !ok {
			t.Fatalf("entry = %+v, want a map", av)
		}
		if _, ok := entry["id"].(string); !ok {
			t.Errorf("entry %+v: id should stay a string", entry)
		}
		if _, ok := entry["value"].(bool); !ok {
			t.Errorf("entry %+v: value should be a real JSON boolean", entry)
		}
	}
}
