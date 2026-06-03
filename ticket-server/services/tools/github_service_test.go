package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nudgebee/tickets-server/models"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v67/github"
)

// newTestGithubClient wires a github.Client to a local httptest server so we
// can drive both the issues/assignees lookup and the issue-create endpoint
// against canned responses.
func newTestGithubClient(t *testing.T, handler http.Handler) (*github.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	baseURL, err := url.Parse(srv.URL + "/")
	if err != nil {
		srv.Close()
		t.Fatalf("parse test server URL: %v", err)
	}
	client := github.NewClient(nil)
	client.BaseURL = baseURL
	client.UploadURL = baseURL
	return client, srv
}

// TestCreateGithubIssueWithClient_RejectsInvalidAssignee verifies that we
// surface a clear error (with the list of valid assignees) when the LLM/UI
// passes an assignee that isn't a collaborator on the repo, instead of
// letting GitHub silently drop the field and reporting a phantom assignment.
func TestCreateGithubIssueWithClient_RejectsInvalidAssignee(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var createCalled bool

	handler := http.NewServeMux()
	// IsAssignee for the invalid user → 404
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/assignees/ghost", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// ListAssignees returns two valid users for the error message
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/assignees", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"login":"alice","id":1},{"login":"bob","id":2}]`))
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(_ http.ResponseWriter, _ *http.Request) {
		createCalled = true
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ticket := models.Ticket{
		Title:      "boom",
		ProjectKey: owner + "/" + repo,
		Assignee:   "ghost",
		Source:     "ui",
	}

	_, err := createGithubIssueWithClient(ctx, client, ticket)
	if err == nil {
		t.Fatal("expected error for invalid assignee, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the invalid assignee, got: %v", err)
	}
	if !strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "bob") {
		t.Errorf("error should list valid assignees (alice, bob), got: %v", err)
	}
	if createCalled {
		t.Error("issue should not be created when assignee validation fails")
	}
}

// TestCreateGithubIssueWithClient_OmitsEmptyAssignee ensures that when no
// assignee is requested we don't serialize an empty-string Assignee field
// (which GitHub would reject) and skip the validation API call entirely.
func TestCreateGithubIssueWithClient_OmitsEmptyAssignee(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var capturedAssigneeKey bool
	var assigneeCheckCalled bool

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/assignees/", func(_ http.ResponseWriter, _ *http.Request) {
		assigneeCheckCalled = true
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		// Inspect the JSON body for an "assignee" key
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		_, capturedAssigneeKey = body["assignee"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/nudgebee/demo/issues/7"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ticket := models.Ticket{
		Title:      "no assignee",
		ProjectKey: owner + "/" + repo,
		Source:     "ui",
	}

	got, err := createGithubIssueWithClient(ctx, client, ticket)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TicketID != "7" {
		t.Errorf("expected ticket_id=7, got %q", got.TicketID)
	}
	if assigneeCheckCalled {
		t.Error("IsAssignee should not be called when no assignee is requested")
	}
	if capturedAssigneeKey {
		t.Error("issue body should not contain an assignee key when none is requested")
	}
}

// TestCreateGithubIssueWithClient_AcceptsValidAssignee verifies the happy
// path: a valid assignee passes validation, gets included in the create
// request, and the issue is created.
func TestCreateGithubIssueWithClient_AcceptsValidAssignee(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentAssignee string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/assignees/alice", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Assignee string `json:"assignee"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentAssignee = body.Assignee
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/nudgebee/demo/issues/42"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ticket := models.Ticket{
		Title:      "real bug",
		ProjectKey: owner + "/" + repo,
		Assignee:   "alice",
		Source:     "ui",
	}

	got, err := createGithubIssueWithClient(ctx, client, ticket)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentAssignee != "alice" {
		t.Errorf("expected assignee=alice sent to GitHub, got %q", sentAssignee)
	}
	if got.TicketID != "42" {
		t.Errorf("expected ticket_id=42, got %q", got.TicketID)
	}
	if got.Status != "open" {
		t.Errorf("expected status=open, got %q", got.Status)
	}
}
