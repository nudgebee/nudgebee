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
