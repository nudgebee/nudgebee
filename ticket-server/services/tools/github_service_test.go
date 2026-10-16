package tools

import (
	"encoding/base64"
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

// A pasted/dragged image in GitHub's comment box produces a raw <img> tag
// pointing at github.com/user-attachments/. extractGithubUserAttachments must
// pull it into a real attachment (resolvable later via GetAttachment) and
// replace it in the body with a position marker - not strip it outright -
// so the frontend can put the image back where it actually was.
func TestExtractGithubUserAttachments_ImgTag(t *testing.T) {
	body := "This is another comments with Image\n" +
		`<img width="1183" height="352" alt="Image" src="https://github.com/user-attachments/assets/165c956c-6d7f-438b-9773-7d9b57304c23" />` + "\n" +
		"Should be visible in Nudgebee\n"

	gotBody, attachments := extractGithubUserAttachments(body)

	wantBody := "This is another comments with Image\n[[nb-attachment:165c956c-6d7f-438b-9773-7d9b57304c23]]\nShould be visible in Nudgebee\n"
	if gotBody != wantBody {
		t.Fatalf("got body %q, want %q", gotBody, wantBody)
	}
	if len(attachments) != 1 || attachments[0].ID != "165c956c-6d7f-438b-9773-7d9b57304c23" || attachments[0].Name != "Image" {
		t.Fatalf("attachments = %+v, want one with id=165c956c-... name=Image", attachments)
	}
}

// Same CDN referenced via markdown ![]() syntax (manually written, or pasted
// from GitHub's "copy image address") must be extracted identically.
func TestExtractGithubUserAttachments_MarkdownSyntax(t *testing.T) {
	gotBody, attachments := extractGithubUserAttachments("![hey](https://github.com/user-attachments/assets/165c956c-6d7f-438b-9773-7d9b57304c23)")

	wantBody := "[[nb-attachment:165c956c-6d7f-438b-9773-7d9b57304c23]]"
	if gotBody != wantBody {
		t.Fatalf("got body %q, want %q", gotBody, wantBody)
	}
	if len(attachments) != 1 || attachments[0].ID != "165c956c-6d7f-438b-9773-7d9b57304c23" || attachments[0].Name != "hey" {
		t.Fatalf("attachments = %+v, want one with id=165c956c-... name=hey", attachments)
	}
}

// A host extractGithubUserAttachments doesn't own must pass through untouched.
func TestExtractGithubUserAttachments_LeavesOtherHostsAlone(t *testing.T) {
	body := "![a screenshot](https://raw.githubusercontent.com/nudgebee/nudgebee-enterprise/main/x.png)"
	gotBody, attachments := extractGithubUserAttachments(body)
	if gotBody != body || len(attachments) != 0 {
		t.Fatalf("got body=%q attachments=%+v, want unchanged with no attachments", gotBody, attachments)
	}
}

// The JWT-signed private-user-images.githubusercontent.com mirror must also be
// rewritten, even though it's a different (non-session-gated) mechanism - its
// signature carries a 5-minute TTL, so by the time Nudgebee reads the comment
// it's realistically always already expired.
func TestRewriteUnembeddableGithubImages_SignedMirrorURL(t *testing.T) {
	signedURL := "https://private-user-images.githubusercontent.com/114298732/665514036-165c956c.png?jwt=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJleHAiOjE3OTExOTE3Mjl9.sig"
	got := rewriteUnembeddableGithubImages(fmt.Sprintf("![img](%s)", signedURL))

	want := fmt.Sprintf("[\U0001F4CE img (view on GitHub)](%s)", signedURL)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A normal markdown image or an <img> from any other host must pass through
// unchanged - only the specific unembeddable GitHub CDN is rewritten.
func TestRewriteUnembeddableGithubImages_LeavesOtherImagesAlone(t *testing.T) {
	body := "![a screenshot](https://raw.githubusercontent.com/nudgebee/nudgebee-enterprise/main/x.png) and " +
		`<img src="https://example.com/other.png" alt="other" />`

	got := rewriteUnembeddableGithubImages(body)
	if got != body {
		t.Fatalf("expected unrelated images to pass through unchanged, got %q", got)
	}
}

// A dragged/pasted video produces a <video src="..."> tag (GitHub has no
// markdown video syntax), pointing at either unembeddable CDN. Unlike images,
// video is never resolved into a real player - just a link.
func TestRewriteGithubVideoTags_UserAttachments(t *testing.T) {
	body := `<video src="https://github.com/user-attachments/assets/165c956c-6d7f-438b-9773-7d9b57304c23" controls="controls" muted="muted"></video>`

	got := rewriteGithubVideoTags(body)

	want := "[\U0001F3AC attached video (view on GitHub)](https://github.com/user-attachments/assets/165c956c-6d7f-438b-9773-7d9b57304c23)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRewriteGithubVideoTags_SignedMirrorURL(t *testing.T) {
	signedURL := "https://private-user-images.githubusercontent.com/114298732/665514036-165c956c.mp4?jwt=eyJhbGciOiJIUzI1NiJ9.sig"
	got := rewriteGithubVideoTags(fmt.Sprintf(`<video src="%s"></video>`, signedURL))

	want := fmt.Sprintf("[\U0001F3AC attached video (view on GitHub)](%s)", signedURL)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A <video> from any other host must pass through unchanged.
func TestRewriteGithubVideoTags_LeavesOtherHostsAlone(t *testing.T) {
	body := `<video src="https://example.com/clip.mp4"></video>`
	got := rewriteGithubVideoTags(body)
	if got != body {
		t.Fatalf("expected unrelated video to pass through unchanged, got %q", got)
	}
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
		wantStatus  string
		wantErr     string
	}{
		{name: "plain case insensitive", status: "in progress", itemProject: "P1", wantOption: "O1", wantStatus: "In Progress"},
		{name: "encoded selector", status: "github-project:P1:F1:O2", itemProject: "P1", secondItem: true, wantOption: "O2", wantStatus: "Done"},
		{name: "plain punctuation", status: "Ready: QA", itemProject: "P1", wantOption: "O1", wantStatus: "Ready: QA"},
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
					if !strings.Contains(request.Query, "includeArchived: false") {
						t.Errorf("projectItems query missing includeArchived: false: %s", request.Query)
					}
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
					if !strings.Contains(request.Query, "projectId: $projectId") ||
						!strings.Contains(request.Query, "itemId: $itemId") ||
						!strings.Contains(request.Query, "fieldId: $fieldId") ||
						!strings.Contains(request.Query, "singleSelectOptionId: $optionId") ||
						strings.Contains(request.Query, "text:") {
						t.Errorf("mutation request has unexpected Project status input: %s", request.Query)
					}
					mutationVariables = request.Variables
					_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"I1"}}}}`))
				default:
					t.Fatalf("unexpected GraphQL query: %s", request.Query)
				}
			})
			client, srv := newTestGithubClient(t, handler)
			defer srv.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

			gotStatus, err := (&GitHubService{}).updateProjectStatus(ctx, client, "owner", "repo", 7, test.status)
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
			if gotStatus != test.wantStatus {
				t.Fatalf("resolved status = %q, want %q", gotStatus, test.wantStatus)
			}
			for key, want := range map[string]string{"projectId": "P1", "itemId": "I1", "fieldId": "F1", "optionId": test.wantOption} {
				if mutationVariables[key] != want {
					t.Errorf("mutation %s = %v, want %s", key, mutationVariables[key], want)
				}
			}
		})
	}
}

func TestUpdateProjectStatusIgnoresArchivedItems(t *testing.T) {
	tests := []struct {
		name          string
		status        string
		mockItemsJSON string
		wantMutation  bool
		wantProjectID string
		wantItemID    string
		wantFieldID   string
		wantOptionID  string
		wantStatus    string
		wantErr       string
	}{
		{
			name:   "archived plus active match ignores archived item and updates active item",
			status: "In Progress",
			// P1 is an old sprint board where item I1 is archived. P2 is active sprint board with item I2.
			// Both boards have "In Progress" (O1). Without filtering, this would trigger an ambiguous error.
			// With filtering, I1 is ignored, avoiding false ambiguity and updating I2.
			mockItemsJSON: `{"id":"I1","isArchived":true,"project":{"id":"P1","title":"Sprint 41","number":1}},{"id":"I2","isArchived":false,"project":{"id":"P2","title":"Sprint 42","number":2}}`,
			wantMutation:  true,
			wantProjectID: "P2",
			wantItemID:    "I2",
			wantFieldID:   "F1",
			wantOptionID:  "O1",
			wantStatus:    "In Progress",
		},
		{
			name:   "archived-only match returns not linked error and does not mutate",
			status: "In Progress",
			// Only P1 has the item and it is archived.
			mockItemsJSON: `{"id":"I1","isArchived":true,"project":{"id":"P1","title":"Sprint 41","number":1}}`,
			wantMutation:  false,
			wantErr:       "not linked",
		},
		{
			name:          "archived-only match with encoded selector returns not linked error and does not mutate",
			status:        "github-project:P1:F1:O1",
			mockItemsJSON: `{"id":"I1","isArchived":true,"project":{"id":"P1","title":"Sprint 41","number":1}}`,
			wantMutation:  false,
			wantErr:       "not linked",
		},
		{
			name:   "server-side includeArchived false omits archived items",
			status: "In Progress",
			// Server responds to includeArchived: false by omitting archived items altogether (returning only I2).
			mockItemsJSON: `{"id":"I2","isArchived":false,"project":{"id":"P2","title":"Sprint 42","number":2}}`,
			wantMutation:  true,
			wantProjectID: "P2",
			wantItemID:    "I2",
			wantFieldID:   "F1",
			wantOptionID:  "O1",
			wantStatus:    "In Progress",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mutationVariables map[string]interface{}
			var queryChecked bool

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
					queryChecked = true
					if !strings.Contains(request.Query, "includeArchived: false") {
						t.Errorf("projectItems query missing includeArchived: false: %s", request.Query)
					}
					_, _ = fmt.Fprintf(w, `{"data":{"repository":{"issue":{"projectItems":{"nodes":[%s],"pageInfo":{"hasNextPage":false}}}}}}`, tc.mockItemsJSON)
				case strings.Contains(request.Query, "fields(first"):
					// Both P1 and P2 have a Status field with "In Progress" (O1) and "Done" (O2)
					_, _ = w.Write([]byte(`{"data":{"node":{"fields":{"nodes":[{"id":"F1","name":"Status","options":[{"id":"O1","name":"In Progress"},{"id":"O2","name":"Done"}]}],"pageInfo":{"hasNextPage":false}}}}}`))
				case strings.Contains(request.Query, "updateProjectV2ItemFieldValue"):
					mutationVariables = request.Variables
					_, _ = w.Write([]byte(`{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"I2"}}}}`))
				default:
					t.Fatalf("unexpected GraphQL query: %s", request.Query)
				}
			})

			client, srv := newTestGithubClient(t, handler)
			defer srv.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

			gotStatus, err := (&GitHubService{}).updateProjectStatus(ctx, client, "owner", "repo", 7, tc.status)
			if !queryChecked {
				t.Fatal("expected projectItems query to be executed")
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				if mutationVariables != nil {
					t.Fatal("mutation must NOT be called when matching fails or only archived item matches")
				}
				return
			}
			if err != nil {
				t.Fatalf("updateProjectStatus() error = %v", err)
			}
			if gotStatus != tc.wantStatus {
				t.Fatalf("resolved status = %q, want %q", gotStatus, tc.wantStatus)
			}
			if tc.wantMutation {
				if mutationVariables == nil {
					t.Fatal("expected mutation to be called, but it was not")
				}
				for key, want := range map[string]string{
					"projectId": tc.wantProjectID,
					"itemId":    tc.wantItemID,
					"fieldId":   tc.wantFieldID,
					"optionId":  tc.wantOptionID,
				} {
					if mutationVariables[key] != want {
						t.Errorf("mutation %s = %v, want %s", key, mutationVariables[key], want)
					}
				}
			} else if mutationVariables != nil {
				t.Fatal("mutation must NOT be called")
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

// --- Issue-form template parsing (.github/ISSUE_TEMPLATE/*.yml) ---

const testGithubIssueFormYAML = `
name: Feature Request
body:
  - type: markdown
    attributes:
      value: "Thanks for filing!"
  - type: input
    id: contact
    attributes:
      label: Contact Details
    validations:
      required: false
  - type: textarea
    id: what-happened
    attributes:
      label: What happened?
    validations:
      required: true
  - type: dropdown
    id: version
    attributes:
      label: Version
      options:
        - "1.0"
        - "2.0"
    validations:
      required: true
  - type: checkboxes
    id: terms
    attributes:
      label: Checklist
      options:
        - label: I agree
          required: true
        - label: Optional item
`

// A realistic multi-field form parses into one field per non-markdown body
// item, typed and ordered to match the YAML; markdown is skipped.
func TestParseGithubIssueFormYAML_FieldTypesAndOrder(t *testing.T) {
	tpl, ok := parseGithubIssueFormYAML("feature_request.yml", testGithubIssueFormYAML)
	if !ok {
		t.Fatal("expected ok=true for a well-formed form")
	}
	if tpl.Name != "Feature Request" {
		t.Errorf("name = %q, want %q", tpl.Name, "Feature Request")
	}
	wantOrder := []string{"contact", "what_happened", "version", "terms"}
	if fmt.Sprint(tpl.Order) != fmt.Sprint(wantOrder) {
		t.Fatalf("order = %v, want %v", tpl.Order, wantOrder)
	}

	contact, ok := tpl.Fields["contact"]
	if !ok || contact.Type != "string" || contact.Required {
		t.Errorf("contact field = %+v, want type=string required=false", contact)
	}

	whatHappened, ok := tpl.Fields["what_happened"]
	if !ok || whatHappened.Type != "text" || !whatHappened.Required {
		t.Errorf("what_happened field = %+v, want type=text required=true", whatHappened)
	}

	version, ok := tpl.Fields["version"]
	if !ok || version.Type != "select" || !version.Required || len(version.AllowedValues) != 2 {
		t.Errorf("version field = %+v, want type=select required=true with 2 options", version)
	}

	terms, ok := tpl.Fields["terms"]
	if !ok || terms.Type != "multiselect" || !terms.Required || len(terms.AllowedValues) != 2 {
		t.Errorf("terms field = %+v, want type=multiselect required=true with 2 options", terms)
	}
}

// A form with no "description"-keyed item, but with a "summary"-keyed one
// (e.g. a Spike Request's "Summary" question - GitHub's real issue title
// always comes from the separate top-level title: key, never a body item,
// so this is never a title duplicate), binds that field directly onto the
// static Description slot instead of rendering a separate duplicate-looking
// field: the UI shows one Description box, and submitting it answers the
// form's actual Summary question.
func TestParseGithubIssueFormYAML_SummaryAliasedAsDescriptionWhenNoDescriptionField(t *testing.T) {
	yamlSrc := `
name: Spike Request
body:
  - type: textarea
    id: summary
    attributes:
      label: Summary
    validations:
      required: true
  - type: textarea
    id: objectives
    attributes:
      label: Objectives
    validations:
      required: true
`
	tpl, ok := parseGithubIssueFormYAML("spike_request.yml", yamlSrc)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if _, renamed := tpl.Fields["summary_2"]; renamed {
		t.Fatal("summary must not also render as its own renamed field when aliased onto description")
	}
	bound, ok := tpl.Fields["description"]
	if !ok || bound.Type != "text" || !bound.Required || bound.Name != "Summary" {
		t.Fatalf("expected summary bound onto the description key (type=text required=true name=Summary), got %+v", bound)
	}
	// "description" must still appear in Order, at the position the aliased
	// field actually held in the form (here, first) - so the created issue's
	// body places it where the form declared it, not always first regardless
	// of the form's real layout.
	wantOrder := []string{"description", "objectives"}
	if fmt.Sprint(tpl.Order) != fmt.Sprint(wantOrder) {
		t.Fatalf("order = %v, want %v", tpl.Order, wantOrder)
	}
	if _, ok := tpl.Fields["objectives"]; !ok {
		t.Fatalf("expected the non-colliding objectives field to be kept, got fields=%+v", tpl.Fields)
	}
}

// A real-world layout (e.g. a Feature Request form: Function, Who asked for
// this, Summary, What this unblocks) rarely puts its Description/Summary
// question first - Order must capture its true middle position so the
// created issue's body matches the form, not GetCreateMeta's parse order.
func TestParseGithubIssueFormYAML_AliasedFieldKeepsItsMidFormPosition(t *testing.T) {
	yamlSrc := `
name: Feature Request
body:
  - type: dropdown
    id: function
    attributes:
      label: Function
      options:
        - Troubleshooting
        - Optimize
    validations:
      required: true
  - type: dropdown
    id: who_asked_for_this
    attributes:
      label: Who asked for this
      options:
        - Customer
        - Our own judgement
    validations:
      required: true
  - type: textarea
    id: summary
    attributes:
      label: Summary
    validations:
      required: true
  - type: textarea
    id: what_this_unblocks
    attributes:
      label: What this unblocks
    validations:
      required: true
`
	tpl, ok := parseGithubIssueFormYAML("feature_request.yml", yamlSrc)
	if !ok {
		t.Fatal("expected ok=true")
	}
	wantOrder := []string{"function", "who_asked_for_this", "description", "what_this_unblocks"}
	if fmt.Sprint(tpl.Order) != fmt.Sprint(wantOrder) {
		t.Fatalf("order = %v, want %v (description/Summary at its real 3rd position)", tpl.Order, wantOrder)
	}
}

// A form with distinct "description" and "summary" items keeps both visible:
// "description" is bound onto the static Description slot (priority), while
// "summary" still renders as its own renamed dynamic field rather than being
// dropped or silently merged away.
func TestParseGithubIssueFormYAML_DescriptionTakesPriorityOverSummaryBothShown(t *testing.T) {
	yamlSrc := `
name: Bug Report
body:
  - type: textarea
    id: summary
    attributes:
      label: Summary
    validations:
      required: true
  - type: textarea
    id: description
    attributes:
      label: Description
    validations:
      required: true
`
	tpl, ok := parseGithubIssueFormYAML("bug_report.yml", yamlSrc)
	if !ok {
		t.Fatal("expected ok=true")
	}
	bound, ok := tpl.Fields["description"]
	if !ok || bound.Name != "Description" {
		t.Fatalf("expected the description item bound onto the description key, got %+v", bound)
	}
	renamedSummary, ok := tpl.Fields["summary_2"]
	if !ok || renamedSummary.Name != "Summary" || !renamedSummary.Required {
		t.Fatalf("expected summary to still render separately as summary_2, got fields=%+v", tpl.Fields)
	}
}

// A form with only markdown body items has no answerable fields, so the
// caller falls back to the plain static template rather than offering an
// empty form.
func TestParseGithubIssueFormYAML_MarkdownOnlyReturnsFalse(t *testing.T) {
	yamlSrc := `
name: Just Info
body:
  - type: markdown
    attributes:
      value: "Please read our docs first."
`
	if _, ok := parseGithubIssueFormYAML("info.yml", yamlSrc); ok {
		t.Fatal("expected ok=false for a form with no input fields")
	}
}

func TestParseGithubIssueFormYAML_InvalidYAMLReturnsFalse(t *testing.T) {
	if _, ok := parseGithubIssueFormYAML("broken.yml", "not: valid: yaml: ["); ok {
		t.Fatal("expected ok=false for malformed YAML")
	}
}

// fetchGithubIssueFormTemplates skips config.yml (the chooser config, not a
// form) and a malformed form file, while still returning the valid ones.
func TestFetchGithubIssueFormTemplates_SkipsConfigAndMalformed(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	dirListing := `[
		{"type":"file","name":"config.yml","path":".github/ISSUE_TEMPLATE/config.yml"},
		{"type":"file","name":"feature_request.yml","path":".github/ISSUE_TEMPLATE/feature_request.yml"},
		{"type":"file","name":"broken.yml","path":".github/ISSUE_TEMPLATE/broken.yml"},
		{"type":"file","name":"PULL_REQUEST_TEMPLATE.md","path":".github/ISSUE_TEMPLATE/PULL_REQUEST_TEMPLATE.md"}
	]`

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dirListing))
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE/feature_request.yml", func(w http.ResponseWriter, r *http.Request) {
		writeGithubFileContent(w, testGithubIssueFormYAML)
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE/broken.yml", func(w http.ResponseWriter, r *http.Request) {
		writeGithubFileContent(w, "not: valid: yaml: [")
	})
	// config.yml and the .md file must never be fetched for content - if they
	// are, that is a bug (they would otherwise 404 since no handler is
	// registered for them).

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	templates, err := fetchGithubIssueFormTemplates(ctx, client, owner, repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(templates) != 1 || templates[0].Name != "Feature Request" {
		t.Fatalf("templates = %+v, want exactly one parsed Feature Request template", templates)
	}
}

// A repo with no .github/ISSUE_TEMPLATE directory at all (404) is not an
// error - FetchGitHubIssueCreateMeta's caller falls back to the plain static
// template for it.
func TestFetchGithubIssueFormTemplates_NoDirectoryReturnsNilNil(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	templates, err := fetchGithubIssueFormTemplates(ctx, client, owner, repo)
	if err != nil || templates != nil {
		t.Fatalf("templates=%v err=%v, want nil,nil", templates, err)
	}
}

func writeGithubFileContent(w http.ResponseWriter, content string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, encoded)
}

// --- Rendering submitted form answers into the issue body ---

// With no order supplied (e.g. a pre-this-feature cached create-meta, or a
// stray key order never knew about), sections fall back to alphabetical -
// stable, even if it does not match the original form's layout.
func TestFormatGithubIssueFormAnswers_FallsBackToAlphabeticalWithNoOrder(t *testing.T) {
	fields := map[string]interface{}{
		"what_happened": "It broke",
		"version":       "2.0",
		"labels":        []interface{}{"nudgebee", "bug"},
	}
	got := formatGithubIssueFormAnswers(fields, nil, nil)
	want := "### Version\n\n2.0\n\n### What Happened\n\nIt broke"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Reproduces the real reported issue: a field whose id is a short internal
// slug ("reprod") but whose real form label is "Reproduction steps" must
// show the real label as its heading, not a title-cased guess from the id.
// A key with no entry in labels still falls back to the key-derived heading.
func TestFormatGithubIssueFormAnswers_PrefersRealLabelOverKeyGuess(t *testing.T) {
	fields := map[string]interface{}{
		"reprod":     "1. Go to '...'\n2. Click on '...'",
		"reprod_url": "N/A",
		"impact":     "No impact",
	}
	labels := map[string]string{
		"reprod":     "Reproduction steps",
		"reprod_url": "Reproduction URL",
	}
	got := formatGithubIssueFormAnswers(fields, []string{"reprod", "reprod_url", "impact"}, labels)
	want := "### Reproduction steps\n\n1. Go to '...'\n2. Click on '...'" +
		"\n\n### Reproduction URL\n\nN/A" +
		"\n\n### Impact\n\nNo impact"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// When the frontend sends the matching Template's field order, sections must
// follow it (the form's declared layout) rather than alphabetical - this is
// the whole point of threading order through extractGithubFieldOrder.
func TestFormatGithubIssueFormAnswers_HonorsSuppliedOrder(t *testing.T) {
	fields := map[string]interface{}{
		"what_happened": "It broke",
		"version":       "2.0",
	}
	got := formatGithubIssueFormAnswers(fields, []string{"what_happened", "version"}, nil)
	want := "### What Happened\n\nIt broke\n\n### Version\n\n2.0"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A key order doesn't cover (e.g. a reserved-key-renamed field added after
// the order was computed, or stale cached order missing a newer field)
// still renders - appended after the ordered ones, alphabetically.
func TestFormatGithubIssueFormAnswers_UnorderedKeysAppendedAfter(t *testing.T) {
	fields := map[string]interface{}{
		"what_happened": "It broke",
		"version":       "2.0",
		"extra_field":   "surprise",
	}
	got := formatGithubIssueFormAnswers(fields, []string{"what_happened", "version"}, nil)
	want := "### What Happened\n\nIt broke\n\n### Version\n\n2.0\n\n### Extra Field\n\nsurprise"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A multiselect (checkboxes) field's selections arrive from the frontend as
// {"id": ...} objects, not plain strings (TicketFormComponent's handleChange
// wraps every multiselect value that way, for every provider) - these must
// render as their id, not as a stringified Go map.
func TestFormatGithubIssueFormAnswers_UnwrapsMultiselectIDObjects(t *testing.T) {
	fields := map[string]interface{}{
		"terms": []interface{}{
			map[string]interface{}{"id": "I agree"},
			map[string]interface{}{"id": "Optional item"},
		},
	}
	got := formatGithubIssueFormAnswers(fields, nil, nil)
	want := "### Terms\n\nI agree, Optional item"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatGithubIssueFormAnswers_EmptyAndNilValuesOmitted(t *testing.T) {
	fields := map[string]interface{}{
		"empty": "",
		"nil":   nil,
		"set":   "value",
	}
	got := formatGithubIssueFormAnswers(fields, nil, nil)
	want := "### Set\n\nvalue"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// createGithubIssueWithClient folds any additionalFields beyond
// assignee/labels into the body as markdown sections, since the plain Issues
// API has no slot for them.
func TestCreateGithubIssueWithClient_AppendsFormAnswersToBody(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentBody string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentBody = body.Body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":9,"html_url":"https://github.com/nudgebee/demo/issues/9"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:       "form-driven issue",
		Description: "Free-text description",
		ProjectKey:  owner + "/" + repo,
		Source:      "ui",
		AdditionalFields: map[string]interface{}{
			"what_happened": "Button does nothing",
		},
	}

	if _, err := createGithubIssueWithClient(ctx, client, tkt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBody := "### Description\n\nFree-text description\n\n---\n\n### What Happened\n\nButton does nothing"
	if sentBody != wantBody {
		t.Fatalf("body = %q, want %q", sentBody, wantBody)
	}
}

// CreateIssue persists the returned ticket's Description verbatim to
// NudgeBee's own DB (nothing re-fetches from GitHub to redisplay it - the
// Tickets tab reads tickets_list, a plain DB query) - so the returned
// ticket.Description must be the composed body actually sent to GitHub, not
// the original free text, or the local record would be missing every
// dynamic form answer the real issue contains.
func TestCreateGithubIssueWithClient_ReturnsComposedBodyAsDescription(t *testing.T) {
	const owner, repo = "nudgebee", "demo"

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":13,"html_url":"https://github.com/nudgebee/demo/issues/13"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:       "form-driven issue",
		Description: "Free-text description",
		ProjectKey:  owner + "/" + repo,
		Source:      "ui",
		AdditionalFields: map[string]interface{}{
			"what_happened": "Button does nothing",
		},
	}

	got, err := createGithubIssueWithClient(ctx, client, tkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantDescription := "### Description\n\nFree-text description\n\n---\n\n### What Happened\n\nButton does nothing"
	if got.Description != wantDescription {
		t.Fatalf("returned ticket.Description = %q, want %q", got.Description, wantDescription)
	}
}

// A plain ticket with no dynamic form answers keeps its body as the raw
// description text, unheaded - the "### Description" heading only earns its
// place when there are other headed sections for it to sit alongside (see
// TestCreateGithubIssueWithClient_AppendsFormAnswersToBody).
func TestCreateGithubIssueWithClient_PlainTicketBodyStaysUnheaded(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentBody string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentBody = body.Body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":10,"html_url":"https://github.com/nudgebee/demo/issues/10"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:       "plain issue",
		Description: "Just a plain description",
		ProjectKey:  owner + "/" + repo,
		Source:      "ui",
	}

	if _, err := createGithubIssueWithClient(ctx, client, tkt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentBody != "Just a plain description" {
		t.Fatalf("body = %q, want unheaded plain description", sentBody)
	}
}

// The frontend-supplied _field_order (see extractGithubFieldOrder) must
// drive the body's section order, matching the form's declared layout
// instead of falling back to alphabetical, and must never itself render as
// a field (it would otherwise show up as a nonsense "### Field Order"
// section, or leak into the labels GitHub actually receives).
func TestCreateGithubIssueWithClient_HonorsFieldOrderAndStripsIt(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentBody string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentBody = body.Body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":11,"html_url":"https://github.com/nudgebee/demo/issues/11"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:      "order-driven issue",
		ProjectKey: owner + "/" + repo,
		Source:     "ui",
		AdditionalFields: map[string]interface{}{
			// Declared out of alphabetical order on purpose: alphabetical
			// would put "environment" before "what_happened".
			"what_happened": "Button does nothing",
			"environment":   "Production",
			"_field_order":  []interface{}{"what_happened", "environment"},
		},
	}

	if _, err := createGithubIssueWithClient(ctx, client, tkt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBody := "### What Happened\n\nButton does nothing\n\n### Environment\n\nProduction"
	if sentBody != wantBody {
		t.Fatalf("body = %q, want %q (order honored, _field_order stripped)", sentBody, wantBody)
	}
}

// End-to-end version of the real reported bug: a field keyed "reprod" with
// real label "Reproduction steps" must show that label in the created
// issue's body, and _field_labels must never itself render as a field.
func TestCreateGithubIssueWithClient_UsesRealLabelsAndStripsThem(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentBody string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentBody = body.Body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":14,"html_url":"https://github.com/nudgebee/demo/issues/14"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:      "labeled issue",
		ProjectKey: owner + "/" + repo,
		Source:     "ui",
		AdditionalFields: map[string]interface{}{
			"reprod":       "1. Go to '...'\n2. See error",
			"_field_order": []interface{}{"reprod"},
			"_field_labels": map[string]interface{}{
				"reprod": "Reproduction steps",
			},
		},
	}

	if _, err := createGithubIssueWithClient(ctx, client, tkt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBody := "### Reproduction steps\n\n1. Go to '...'\n2. See error"
	if sentBody != wantBody {
		t.Fatalf("body = %q, want %q (real label used, _field_labels stripped)", sentBody, wantBody)
	}
}

// Reproduces the real reported scenario: a Feature Request form whose
// Summary (aliased onto Description) is its 3rd field, not its 1st. The
// created issue's body must place ticket.Description there too, not always
// first - this is what distinguishes descriptionHasOrderedPosition's path
// from the legacy always-first fallback.
func TestCreateGithubIssueWithClient_PlacesDescriptionAtItsFormPosition(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	var sentBody string

	handler := http.NewServeMux()
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		sentBody = body.Body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":12,"html_url":"https://github.com/nudgebee/demo/issues/12"}`))
	})

	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	tkt := models.Ticket{
		Title:       "feature-request-driven issue",
		Description: "Provide a brief explanation of the feature.",
		ProjectKey:  owner + "/" + repo,
		Source:      "ui",
		AdditionalFields: map[string]interface{}{
			"function":           "Troubleshooting",
			"who_asked_for_this": "Customer",
			"what_this_unblocks": "Blocks the Acme renewal",
			"_field_order":       []interface{}{"function", "who_asked_for_this", "description", "what_this_unblocks"},
		},
	}

	if _, err := createGithubIssueWithClient(ctx, client, tkt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBody := "### Function\n\nTroubleshooting" +
		"\n\n### Who Asked For This\n\nCustomer" +
		"\n\n### Description\n\nProvide a brief explanation of the feature." +
		"\n\n### What This Unblocks\n\nBlocks the Acme renewal"
	if sentBody != wantBody {
		t.Fatalf("body =\n%s\nwant:\n%s", sentBody, wantBody)
	}
}

// --- fetchGitHubIssueCreateMetaWithClient: templates offered end to end ---

func githubCreateMetaHandler(owner, repo string, mux *http.ServeMux) {
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/assignees", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"login":"alice","id":1}]`))
	})
	mux.HandleFunc("/repos/"+owner+"/"+repo+"/labels", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"bug"}]`))
	})
}

func createMetaTemplateNames(t *testing.T, meta any) []string {
	t.Helper()
	data, ok := meta.(map[string]interface{})
	if !ok {
		t.Fatalf("meta is %T, want map[string]interface{}", meta)
	}
	templates, ok := data["data"].([]Template)
	if !ok {
		t.Fatalf("meta[\"data\"] is %T, want []Template", data["data"])
	}
	names := make([]string, len(templates))
	for i, tpl := range templates {
		names[i] = tpl.Name
	}
	return names
}

// A repo with no .github/ISSUE_TEMPLATE directory still offers exactly one
// plain "Issue" template, unchanged from before this feature existed.
func TestFetchGitHubIssueCreateMetaWithClient_NoForms(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	githubCreateMetaHandler(owner, repo, handler)
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, err := fetchGitHubIssueCreateMetaWithClient(ctx, client, owner+"/"+repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if names := createMetaTemplateNames(t, meta); fmt.Sprint(names) != fmt.Sprint([]string{"📝 Issue"}) {
		t.Fatalf("templates = %v, want [📝 Issue]", names)
	}
}

// Assignee and label option values must be the login/name string a user
// actually selects, not the provider's internal numeric id -
// createGithubIssueWithClient validates/assigns by login and submits label
// names, so a numeric id selected from the dropdown would always fail.
func TestFetchGitHubIssueCreateMetaWithClient_AssigneeAndLabelOptionsUseNameNotNumericID(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	githubCreateMetaHandler(owner, repo, handler)
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, err := fetchGitHubIssueCreateMetaWithClient(ctx, client, owner+"/"+repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := meta.(map[string]interface{})
	templates, _ := data["data"].([]Template)
	if len(templates) != 1 {
		t.Fatalf("templates = %+v, want exactly one", templates)
	}

	assigneeValues := templates[0].Fields["assignee"].AllowedValues
	if len(assigneeValues) != 1 || assigneeValues[0].(map[string]interface{})["id"] != "alice" {
		t.Fatalf("assignee AllowedValues = %+v, want id=\"alice\" (a string login, not a numeric id)", assigneeValues)
	}

	labelValues := templates[0].Fields["labels"].AllowedValues
	if len(labelValues) != 1 || labelValues[0].(map[string]interface{})["id"] != "bug" {
		t.Fatalf("labels AllowedValues = %+v, want id=\"bug\" (a string name, not a numeric id)", labelValues)
	}
}

// A repo with issue-form templates offers the plain "Issue" template *and*
// one per form - a workflow/automation already configured with
// ticket_type: "Issue" must keep working even after forms are added to a repo.
func TestFetchGitHubIssueCreateMetaWithClient_FormsAddedAlongsidePlainIssue(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	githubCreateMetaHandler(owner, repo, handler)
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"file","name":"feature_request.yml","path":".github/ISSUE_TEMPLATE/feature_request.yml"}]`))
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE/feature_request.yml", func(w http.ResponseWriter, r *http.Request) {
		writeGithubFileContent(w, testGithubIssueFormYAML)
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, err := fetchGitHubIssueCreateMetaWithClient(ctx, client, owner+"/"+repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"📝 Issue", "Feature Request"}
	if names := createMetaTemplateNames(t, meta); fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("templates = %v, want %v", names, want)
	}
}

// The per-template Summary->description alias must survive staticFields'
// merge in fetchGitHubIssueCreateMetaWithClient - the generic, optional
// static Description entry must not silently win over it.
func TestFetchGitHubIssueCreateMetaWithClient_SummaryAliasSurvivesStaticFieldsMerge(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	githubCreateMetaHandler(owner, repo, handler)
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"file","name":"spike_request.yml","path":".github/ISSUE_TEMPLATE/spike_request.yml"}]`))
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE/spike_request.yml", func(w http.ResponseWriter, r *http.Request) {
		writeGithubFileContent(w, "name: Spike Request\nbody:\n  - type: textarea\n    id: summary\n    attributes:\n      label: Summary\n    validations:\n      required: true\n")
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, err := fetchGitHubIssueCreateMetaWithClient(ctx, client, owner+"/"+repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := meta.(map[string]interface{})
	templates, _ := data["data"].([]Template)
	var spike *Template
	for i := range templates {
		if templates[i].Name == "Spike Request" {
			spike = &templates[i]
		}
	}
	if spike == nil {
		t.Fatalf("templates = %v, want a Spike Request template", templates)
	}
	description, ok := spike.Fields["description"]
	if !ok || !description.Required || description.Name != "Summary" {
		t.Fatalf("Spike Request's description field = %+v, want the aliased Summary (required=true, name=Summary)", description)
	}
}

// A form literally named "Issue" must not produce a second, indistinguishable
// "Issue" option in the dropdown.
func TestFetchGitHubIssueCreateMetaWithClient_FormNamedIssueIsSkipped(t *testing.T) {
	const owner, repo = "nudgebee", "demo"
	handler := http.NewServeMux()
	githubCreateMetaHandler(owner, repo, handler)
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"type":"file","name":"issue.yml","path":".github/ISSUE_TEMPLATE/issue.yml"}]`))
	})
	handler.HandleFunc("/repos/"+owner+"/"+repo+"/contents/.github/ISSUE_TEMPLATE/issue.yml", func(w http.ResponseWriter, r *http.Request) {
		writeGithubFileContent(w, "name: Issue\nbody:\n  - type: input\n    id: contact\n    attributes:\n      label: Contact\n")
	})
	client, srv := newTestGithubClient(t, handler)
	defer srv.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	meta, err := fetchGitHubIssueCreateMetaWithClient(ctx, client, owner+"/"+repo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if names := createMetaTemplateNames(t, meta); fmt.Sprint(names) != fmt.Sprint([]string{"📝 Issue"}) {
		t.Fatalf("templates = %v, want exactly one Issue template (no duplicate)", names)
	}
}
