package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"nudgebee/tickets-server/models"
	"nudgebee/tickets-server/services/ticket"

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

// TestGetGithubIssueWithClient_MapsMetadata verifies that the Get mapping
// promotes assignees, labels, reporter, milestone, project_key and updated_at
// to top-level Ticket fields (issue #32155), not just into Raw.
func TestGetGithubIssueWithClient_MapsMetadata(t *testing.T) {
	const owner, repo = "nudgebee", "demo"

	handler := http.NewServeMux()
	var accept string
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues/42", func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"number": 42,
			"title": "Issue Title",
			"body": "Issue Description",
			"state": "open",
			"html_url": "https://github.com/nudgebee/demo/issues/42",
			"created_at": "2026-06-11T06:18:55Z",
			"updated_at": "2026-06-11T06:19:05Z",
			"user": {"login": "rohitutekar123"},
			"assignee": {"login": "rohitutekar123"},
			"assignees": [{"login": "Kankshit-02"}, {"login": "rohitutekar123"}],
			"labels": [{"name": "bug"}, {"name": "Workflow"}],
			"milestone": {"title": "v1.0"},
			"issue_field_values": [{"issue_field_name": "Linked incident", "data_type": "text", "value": "PD-1234"}]
		}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	got, err := getGithubIssueWithClient(ctx, client, owner, repo, 42, owner+"/"+repo)
	if err != nil {
		t.Fatalf("getGithubIssueWithClient() error = %v", err)
	}

	if got.Assignee != "rohitutekar123" {
		t.Errorf("Assignee = %q, want rohitutekar123", got.Assignee)
	}
	if joined := strings.Join(got.Assignees, ","); joined != "Kankshit-02,rohitutekar123" {
		t.Errorf("Assignees = %v, want [Kankshit-02 rohitutekar123]", got.Assignees)
	}
	if got.Reporter != "rohitutekar123" {
		t.Errorf("Reporter = %q, want rohitutekar123", got.Reporter)
	}
	if joined := strings.Join(got.Labels, ","); joined != "bug,Workflow" {
		t.Errorf("Labels = %v, want [bug Workflow]", got.Labels)
	}
	if got.Milestone != "v1.0" {
		t.Errorf("Milestone = %q, want v1.0", got.Milestone)
	}
	if got.ProjectKey != owner+"/"+repo {
		t.Errorf("ProjectKey = %q, want %s/%s", got.ProjectKey, owner, repo)
	}
	if got.URL != "https://github.com/nudgebee/demo/issues/42" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.CreatedAt == nil || got.CreatedAt.UTC().Format(time.RFC3339) != "2026-06-11T06:18:55Z" {
		t.Errorf("CreatedAt = %v, want 2026-06-11T06:18:55Z", got.CreatedAt)
	}
	if got.UpdatedAt == nil || got.UpdatedAt.UTC().Format(time.RFC3339) != "2026-06-11T06:19:05Z" {
		t.Errorf("UpdatedAt = %v, want 2026-06-11T06:19:05Z", got.UpdatedAt)
	}
	if got.Platform != "github" {
		t.Errorf("Platform = %q, want github", got.Platform)
	}
	if accept != "application/vnd.github.squirrel-girl-preview" {
		t.Errorf("Accept = %q, want the header Issues.Get sends", accept)
	}
	fields, _ := got.Raw["issue_field_values"].([]any)
	var value any
	if len(fields) == 1 {
		if field, ok := fields[0].(map[string]any); ok {
			value = field["value"]
		}
	}
	if value != "PD-1234" {
		t.Errorf("Raw issue_field_values = %v, want the linked incident value", got.Raw["issue_field_values"])
	}
	if got.Raw["title"] != "Issue Title" {
		t.Errorf("Raw title = %v, want the issue title", got.Raw["title"])
	}
}

// A deleted GitHub issue (410 Gone) or one that never existed (404) must
// surface as ticket.ErrNotFound, not a generic error — the ticket-status
// sync job relies on that to skip it without aborting the rest of the run
// (a stale backlog with deleted issues
// was tripping the sync's consecutive-failure abort guard before it ever
// reached healthy tickets).
func TestGetGithubIssueWithClient_NotFoundAndGoneAreErrNotFound(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const owner, repo = "nudgebee", "demo"
			handler := http.NewServeMux()
			handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues/99", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message": "not found"}`))
			})
			client, srv := newTestGithubClient(t, handler)
			defer srv.Close()

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			_, err := getGithubIssueWithClient(ctx, client, owner, repo, 99, owner+"/"+repo)
			if !errors.Is(err, ticket.ErrNotFound) {
				t.Fatalf("err = %v, want it to wrap ticket.ErrNotFound", err)
			}
		})
	}
}

func TestPageCursorPaginationAndCap(t *testing.T) {
	cursor := "next"
	next, more, err := pageCursor(githubPageInfo{HasNextPage: true, EndCursor: &cursor}, "projects", 1)
	if err != nil || !more || next != cursor {
		t.Fatalf("pageCursor() = (%q, %t, %v), want next cursor", next, more, err)
	}

	_, more, err = pageCursor(githubPageInfo{HasNextPage: true, EndCursor: &cursor}, "projects", maxGitHubGraphQLPages)
	if err == nil || more || !strings.Contains(err.Error(), "pagination limit") {
		t.Fatalf("pageCursor() at cap = (%t, %v), want pagination error", more, err)
	}
}

func TestGithubGraphQLErrorsPermissionCodesOnly(t *testing.T) {
	for _, code := range []string{"INSUFFICIENT_SCOPES", "FORBIDDEN", "NOT_FOUND"} {
		err := githubGraphQLErrors([]githubGraphQLError{{Message: "arbitrary message", Type: code}})
		var permissionErr *githubProjectsPermissionError
		if !errors.As(err, &permissionErr) {
			t.Errorf("code %q should be treated as permission error, got %v", code, err)
		}
	}
	if err := githubGraphQLErrors([]githubGraphQLError{{Message: "forbidden by wording only"}}); err == nil {
		t.Error("message text alone should not trigger permission fallback")
	}
}

func TestProjectStatusValuesUsesInlineOptionsAndExactSelectors(t *testing.T) {
	var graphqlRequests int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		graphqlRequests++
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "projectsV2"):
			_, _ = w.Write([]byte(`{"data":{"repository":{"projectsV2":{"nodes":[{"id":"P1","title":"Board","number":1}],"pageInfo":{"hasNextPage":false}}}}}`))
		case strings.Contains(request.Query, "fields(first"):
			_, _ = w.Write([]byte(`{"data":{"node":{"fields":{"nodes":[{"id":"F1","name":"Status","options":[{"id":"O1","name":"Ready"},{"id":"O2","name":"Done"}]}],"pageInfo":{"hasNextPage":false}}}}}`))
		default:
			t.Fatalf("unexpected GraphQL query: %s", request.Query)
		}
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	values, err := projectStatusValues(ctx, client, "owner", "repo")
	if err != nil {
		t.Fatalf("projectStatusValues() error = %v", err)
	}
	if graphqlRequests != 2 {
		t.Fatalf("GraphQL requests = %d, want 2 (options must be inline)", graphqlRequests)
	}
	status := values[2].(map[string]interface{})
	if status["value"] != "github-project:P1:F1:O1" {
		t.Fatalf("status value = %v, want exact selector", status["value"])
	}
	if status["id"] != "github-project:P1:F1:O1" {
		t.Fatalf("status id = %v, want exact encoded selector", status["id"])
	}
}

func TestUpdateProjectStatusResolvesNamesAndEncodedSelectors(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		itemProject string
		secondItem  bool
		wantOption  string
		wantErr     string
	}{
		{name: "plain case insensitive", status: "in progress", itemProject: "P1", wantOption: "O1"},
		{name: "encoded selector", status: "github-project:P1:F1:O2", itemProject: "P1", secondItem: true, wantOption: "O2"},
		{name: "plain punctuation", status: "Ready: QA", itemProject: "P1", wantOption: "O1"},
		{name: "ambiguous", status: "ready", itemProject: "P1", secondItem: true, wantErr: "ambiguous"},
		{name: "unlinked", status: "QA", itemProject: "P1", wantErr: "not linked"},
		{name: "encoded unlinked project", status: "github-project:P3:F1:O1", itemProject: "P1", wantErr: "not linked"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mutationVariables map[string]interface{}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("decode GraphQL request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(request.Query, "projectItems"):
					items := `{"id":"I1","project":{"id":"P1","title":"Board","number":1}}`
					if test.secondItem {
						items += `,{"id":"I2","project":{"id":"P2","title":"Other","number":2}}`
					}
					_, _ = fmt.Fprintf(w, `{"data":{"repository":{"issue":{"projectItems":{"nodes":[%s],"pageInfo":{"hasNextPage":false}}}}}}`, items)
				case strings.Contains(request.Query, "fields(first"):
					name := "Ready"
					if test.name == "plain case insensitive" {
						name = "In Progress"
					}
					if test.name == "plain punctuation" {
						name = test.status
					}
					if test.name == "unlinked" {
						name = "Ready"
					}
					_, _ = fmt.Fprintf(w, `{"data":{"node":{"fields":{"nodes":[{"id":"F1","name":"Status","options":[{"id":"O1","name":"%s"},{"id":"O2","name":"Done"}]}],"pageInfo":{"hasNextPage":false}}}}}`, name)
				case strings.Contains(request.Query, "updateProjectV2ItemFieldValue"):
					mutationVariables = request.Variables
					_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"I1"}}}}`))
				default:
					t.Fatalf("unexpected GraphQL query: %s", request.Query)
				}
			})
			client, srv := newTestGithubClient(t, handler)
			defer srv.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

			err := (&GitHubService{}).updateProjectStatus(ctx, client, "owner", "repo", 7, test.status)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				if mutationVariables != nil {
					t.Fatal("a rejected Project selection must not mutate the issue")
				}
				return
			}
			if err != nil {
				t.Fatalf("updateProjectStatus() error = %v", err)
			}
			for key, want := range map[string]string{"projectId": "P1", "itemId": "I1", "fieldId": "F1", "optionId": test.wantOption} {
				if mutationVariables[key] != want {
					t.Errorf("mutation %s = %v, want %s", key, mutationVariables[key], want)
				}
			}
		})
	}
}

func TestGitHubProjectPagination(t *testing.T) {
	for _, fetcher := range []struct {
		name  string
		path  []string
		fetch func(*gin.Context, *github.Client) (int, error)
	}{
		{"projects", []string{"data", "repository", "projectsV2"}, func(ctx *gin.Context, client *github.Client) (int, error) {
			rows, err := fetchProjects(ctx, client, "owner", "repo")
			return len(rows), err
		}},
		{"fields", []string{"data", "node", "fields"}, func(ctx *gin.Context, client *github.Client) (int, error) {
			rows, err := fetchProjectFields(ctx, client, "P1")
			return len(rows), err
		}},
		{"items", []string{"data", "repository", "issue", "projectItems"}, func(ctx *gin.Context, client *github.Client) (int, error) {
			rows, err := fetchIssueProjectItems(ctx, client, "owner", "repo", 7)
			return len(rows), err
		}},
	} {
		for _, capped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/capped=%t", fetcher.name, capped), func(t *testing.T) {
				requests := 0
				client, srv := newTestGithubClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					var request struct {
						Variables map[string]interface{} `json:"variables"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if requests == 1 && request.Variables["cursor"] != nil {
						t.Errorf("first cursor = %v", request.Variables["cursor"])
					}
					if requests > 1 && request.Variables["cursor"] != "next" {
						t.Errorf("next cursor = %v", request.Variables["cursor"])
					}
					body := map[string]interface{}{
						"nodes":    []map[string]interface{}{{"id": fmt.Sprintf("row%d", requests)}},
						"pageInfo": map[string]interface{}{"hasNextPage": capped || requests == 1, "endCursor": "next"},
					}
					for i := len(fetcher.path) - 1; i >= 0; i-- {
						body = map[string]interface{}{fetcher.path[i]: body}
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(body); err != nil {
						t.Error(err)
					}
				}))
				defer srv.Close()
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				count, err := fetcher.fetch(ctx, client)
				if capped {
					if err == nil || !strings.Contains(err.Error(), "pagination limit") || requests != maxGitHubGraphQLPages {
						t.Fatalf("count=%d requests=%d error=%v, want bounded pagination failure", count, requests, err)
					}
				} else if err != nil || count != 2 || requests != 2 {
					t.Fatalf("count=%d requests=%d error=%v, want both pages", count, requests, err)
				}
			})
		}
	}
}

func TestGitHubProjectPermissionFallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		rateLimited bool
		fallback    bool
	}{
		{"missing scope", 200, `{"errors":[{"message":"arbitrary", "type":"INSUFFICIENT_SCOPES"}]}`, false, true},
		{"unauthorized", 401, `{"message":"credentials required"}`, false, true},
		{"forbidden", 403, `{"message":"not accessible"}`, false, true},
		{"not found", 404, `{"message":"not found"}`, false, true},
		{"primary rate limit", 403, `{"message":"rate limit"}`, true, false},
		{"mixed GraphQL errors", 200, `{"errors":[{"type":"FORBIDDEN"},{"type":"RATE_LIMITED"}]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, srv := newTestGithubClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.rateLimited {
					w.Header().Set("X-RateLimit-Remaining", "0")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			result, err := fetchGitHubProjectTransitionMetaWithClient(ctx, client, "owner/repo")
			if !tc.fallback {
				if err == nil {
					t.Fatal("non-permission failure must not be hidden as fallback")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			meta := result.(map[string]interface{})
			options := meta["data"].([]interface{})
			if len(options) != 2 || meta["warning"] == "" {
				t.Fatalf("fallback=%v, want open/closed and warning", meta)
			}
		})
	}
}
